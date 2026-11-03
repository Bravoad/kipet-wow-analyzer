package character

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

var Sections = []string{"profile", "equipment", "statistics", "talents"}

// Sections remains the four core inputs consumed by the rule engine. Progress
// is independently optional and must not invalidate an equipment/talent report.
var ProgressSections = []string{"mythic_plus", "raids"}
var AllSections = []string{"profile", "equipment", "statistics", "talents", "mythic_plus", "raids"}

type Key struct {
	Region     string
	Realm      string
	Name       string // NFC(casefold(NFC(display name))).
	LookupName string // Original NFC input, used only for the provider's lowercase path.
}

func NormalizeKey(region, realm, name string) (Key, error) {
	if region != "eu" && region != "us" {
		return Key{}, NewFault("INVALID_ARGUMENT", false)
	}
	if !utf8.ValidString(name) || !utf8.ValidString(realm) {
		return Key{}, NewFault("INVALID_ARGUMENT", false)
	}
	realm = norm.NFC.String(strings.TrimSpace(realm))
	if len(realm) == 0 || utf8.RuneCountInString(realm) > 80 {
		return Key{}, NewFault("INVALID_ARGUMENT", false)
	}
	for _, r := range realm {
		if unicode.IsControl(r) || unicode.IsSpace(r) || strings.ContainsRune("/\\?#%", r) {
			return Key{}, NewFault("INVALID_ARGUMENT", false)
		}
	}
	name = norm.NFC.String(strings.TrimSpace(name))
	if n := utf8.RuneCountInString(name); n < 2 || n > 24 {
		return Key{}, NewFault("INVALID_ARGUMENT", false)
	}
	for i, r := range name {
		if (!unicode.IsLetter(r) && !unicode.IsMark(r)) || (i == 0 && !unicode.IsLetter(r)) {
			return Key{}, NewFault("INVALID_ARGUMENT", false)
		}
	}
	lookupName := name
	name = norm.NFC.String(cases.Fold().String(name))
	if utf8.RuneCountInString(name) > 48 {
		return Key{}, NewFault("INVALID_ARGUMENT", false)
	}
	return Key{Region: region, Realm: realm, Name: name, LookupName: lookupName}, nil
}

type Realm struct {
	ID          string
	ProviderID  string
	Region      string
	Slug        string
	Names       map[string]string
	ConnectedID *string
	FetchedAt   time.Time
}

func (r Realm) DTO(locale string) map[string]any {
	name := r.Names[locale]
	if name == "" {
		name = r.Names["en-US"]
	}
	if name == "" {
		name = r.Names["en_US"]
	}
	if name == "" {
		name = r.Names["en_GB"]
	}
	if name == "" {
		name = r.Slug
	}
	return map[string]any{"id": r.ProviderID, "region": r.Region, "slug": r.Slug, "name": name, "connected_realm_id": r.ConnectedID}
}

type Section struct {
	State           string
	Reason          *string
	FetchedAt       *time.Time
	SourceUpdatedAt *time.Time
	CheckedAt       time.Time
	FreshUntil      *time.Time
	ValidUntil      *time.Time
	Payload         json.RawMessage
}

func (s Section) Publishable(now time.Time) bool {
	return s.State != "unavailable" && s.FetchedAt != nil && s.ValidUntil != nil && now.Before(*s.ValidUntil) && len(s.Payload) > 0
}
func (s Section) Fresh(now time.Time) bool {
	return s.Publishable(now) && s.FreshUntil != nil && now.Before(*s.FreshUntil)
}
func (s Section) Coverage() map[string]any {
	return map[string]any{"state": s.State, "fetched_at": s.FetchedAt, "fresh_until": s.FreshUntil, "valid_until": s.ValidUntil, "reason": s.Reason}
}
func Unavailable(reason string, now time.Time) Section {
	return Section{State: "unavailable", Reason: &reason, CheckedAt: now}
}

type Snapshot struct {
	ID                string
	CharacterID       string
	JobID             string
	ProviderID        string
	DisplayName       string
	FetchedAt         time.Time
	ProfileVerifiedAt time.Time
	ValidUntil        time.Time
	Patch             *string
	Season            *string
	Sections          map[string]Section
}

type Record struct {
	Missing        bool // Batch negative-cache result; never an observed profile.
	DataValidUntil *time.Time
	ID             string
	Key            Key
	Name           string
	ProviderID     *string
	Realm          Realm
	Snapshot       *Snapshot
	Blocked        bool
	LastJob        *Job
}

type Job struct {
	Kind        string
	ID          string
	CharacterID string
	Key         Key
	Status      string
	Reason      string
	Attempts    int
	MaxAttempts int
	RunAfter    time.Time
	CreatedAt   time.Time
	UpdatedAt   time.Time
	FinishedAt  *time.Time
	ErrorCode   *string
}

func (j Job) Active() bool {
	return j.Status == "queued" || j.Status == "running" || j.Status == "retry_wait"
}

type Lease struct {
	Job       Job
	Token     string
	ExpiresAt time.Time
}
type EnqueueResult struct {
	Job     Job
	Created bool
}

type Fault struct {
	Code      string
	Retryable bool
	RetryAt   time.Time
	JobID     *string
}

func (e *Fault) Error() string                    { return e.Code }
func NewFault(code string, retryable bool) *Fault { return &Fault{Code: code, Retryable: retryable} }
func AsFault(err error) *Fault {
	var f *Fault
	if errors.As(err, &f) {
		return f
	}
	return &Fault{Code: "INFRASTRUCTURE_UNAVAILABLE", Retryable: true}
}

var ErrNotFound = errors.New("not found")
var ErrLeaseLost = errors.New("lease lost")

func ValidateSnapshot(s Snapshot) error {
	if s.ProviderID == "" || s.DisplayName == "" || s.Sections == nil {
		return fmt.Errorf("invalid snapshot identity")
	}
	if s.ValidUntil.After(s.ProfileVerifiedAt.Add(720*time.Hour)) || !s.ValidUntil.After(s.FetchedAt) {
		return fmt.Errorf("invalid snapshot retention")
	}
	for _, name := range AllSections {
		v, ok := s.Sections[name]
		if !ok {
			if name == "mythic_plus" || name == "raids" {
				continue
			}
			return fmt.Errorf("missing section %s", name)
		}
		if v.State == "unavailable" {
			if len(v.Payload) > 0 || v.FetchedAt != nil || v.FreshUntil != nil || v.ValidUntil != nil || v.Reason == nil {
				return fmt.Errorf("invalid unavailable section")
			}
			continue
		}
		if v.State != "complete" && v.State != "partial" {
			return fmt.Errorf("invalid section state")
		}
		if v.FetchedAt == nil || v.FreshUntil == nil || v.ValidUntil == nil || !v.FreshUntil.After(*v.FetchedAt) || v.FreshUntil.After(*v.ValidUntil) || v.ValidUntil.After(v.FetchedAt.Add(720*time.Hour)) || !json.Valid(v.Payload) || len(v.Payload) < 2 || v.Payload[0] != '{' {
			return fmt.Errorf("invalid section data")
		}
		if v.State == "complete" && v.Reason != nil || v.State == "partial" && v.Reason == nil {
			return fmt.Errorf("invalid section reason")
		}
	}
	if s.Sections["profile"].State != "complete" {
		return fmt.Errorf("mandatory profile unavailable")
	}
	return nil
}
