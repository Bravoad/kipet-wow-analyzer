package combat

import (
	"encoding/json"
	"math"
	"reflect"
	"strings"
	"testing"
)

func pointer[T any](value T) *T { return &value }
func sample() Collection {
	return Collection{Report: Report{Code: "DEMOCombat000001", Visibility: "public", Fights: []Fight{{ID: 1, ParticipantIDs: []int{1, 2}, StartMS: 1000, EndMS: 121000}}, Actors: []Actor{{ID: 1, Type: "player"}, {ID: 2, Type: "player"}, {ID: 3, Type: "pet"}}}, FightID: 1, ActorID: 1, Complete: true, Events: []Event{}}
}
func TestWholeFightMetricsUseExactActorAndEffectiveHealing(t *testing.T) {
	c := sample()
	c.Events = []Event{
		{TimestampMS: 1100, Kind: "damage", SourceID: pointer(1), TargetID: pointer(100), AbilityID: pointer("11"), Amount: pointer(12000.0)},
		{TimestampMS: 1100, Kind: "damage", SourceID: pointer(3), TargetID: pointer(100), AbilityID: pointer("11"), Amount: pointer(900000.0)},
		{TimestampMS: 1200, Kind: "damage", SourceID: pointer(2), TargetID: pointer(100), AbilityID: pointer("12"), Amount: pointer(900000.0)},
		{TimestampMS: 1300, Kind: "heal", SourceID: pointer(1), TargetID: pointer(2), AbilityID: pointer("21"), Amount: pointer(6000.0), Overheal: pointer(2000.0)},
		{TimestampMS: 1400, Kind: "cast", SourceID: pointer(1), AbilityID: pointer("11")},
		{TimestampMS: 1500, Kind: "interrupt", SourceID: pointer(1), TargetID: pointer(100)},
		{TimestampMS: 1600, Kind: "death", SourceID: pointer(100), TargetID: pointer(1)},
		{TimestampMS: 1700, Kind: "death", SourceID: pointer(1), TargetID: pointer(100)},
	}
	before, _ := json.Marshal(c)
	a, err := Calculate(c, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if a.DurationMS != 120000 || a.Method != Method || *a.Metrics.DPS != 100 || *a.Metrics.HPS != 50 || *a.Metrics.OverhealPercent != 25 || *a.Metrics.Casts != 1 || *a.Metrics.Interrupts != 1 || *a.Metrics.Deaths != 1 {
		t.Fatalf("wrong methodology %+v", a.Metrics)
	}
	if len(a.Abilities) != 2 || a.Abilities[0].ID != "11" || *a.Abilities[0].Damage != 12000 || a.Abilities[0].Casts != 1 || a.Abilities[0].Healing != nil {
		t.Fatalf("wrong attribution %+v", a.Abilities)
	}
	if a.Findings[0].Code != "DEATHS_OBSERVED" || len(a.Findings[0].EventIndexes) != 1 || a.Timeline[a.Findings[0].EventIndexes[0]].TimestampMS != 1600 {
		t.Fatal("death evidence lost")
	}
	after, _ := json.Marshal(c)
	if string(before) != string(after) {
		t.Fatal("calculation mutated inputs")
	}
}
func TestIncompleteAndMissingAmountsNeverBecomeZero(t *testing.T) {
	for _, incomplete := range []bool{false, true} {
		c := sample()
		c.Complete = !incomplete
		c.Events = []Event{{TimestampMS: 2000, Kind: "damage", SourceID: pointer(1), AbilityID: pointer("1")}, {TimestampMS: 2100, Kind: "heal", SourceID: pointer(1), AbilityID: pointer("2"), Amount: pointer(10.0)}}
		a, err := Calculate(c, "fixture")
		if err != nil {
			t.Fatal(err)
		}
		if a.Metrics.DPS != nil || a.Metrics.DamageTotal != nil || a.Metrics.OverhealTotal != nil || a.Abilities[0].Damage != nil {
			t.Fatal("missing amount invented")
		}
		if incomplete && !reflect.DeepEqual(a.Metrics, Metrics{}) {
			t.Fatalf("partial stream has totals %+v", a.Metrics)
		}
		if !incomplete && (a.Metrics.HPS == nil || a.Metrics.Casts == nil || a.Findings[0].Code != "AMOUNTS_UNAVAILABLE") {
			t.Fatal("selectively available metrics lost")
		}
	}
	c := sample()
	a, _ := Calculate(c, "fixture")
	if *a.Metrics.DPS != 0 || *a.Metrics.HPS != 0 || *a.Metrics.Casts != 0 || a.Metrics.OverhealPercent != nil {
		t.Fatal("explicit empty stream not preserved")
	}
}

func TestCompleteActorAmountsWithPartialFightEvents(t *testing.T) {
	c := sample()
	c.Complete = false
	damage := Event{TimestampMS: 1100, Kind: "damage", SourceID: pointer(1), AbilityID: pointer("11"), Amount: pointer(12000.0)}
	c.Events = []Event{damage, {TimestampMS: 1600, Kind: "death", TargetID: pointer(1)}}
	c.ActorAmountEvents = []Event{
		damage,
		{TimestampMS: 110000, Kind: "damage", SourceID: pointer(1), AbilityID: pointer("12"), Amount: pointer(12000.0)},
		{TimestampMS: 111000, Kind: "heal", SourceID: pointer(1), TargetID: pointer(1), Amount: pointer(6000.0), Overheal: pointer(2000.0)},
	}
	a, err := Calculate(c, "warcraftlogs")
	if err != nil {
		t.Fatal(err)
	}
	want := Metrics{
		DamageTotal: pointer(24000.0), DPS: pointer(200.0),
		HealingTotal: pointer(6000.0), HPS: pointer(50.0), OverhealTotal: pointer(2000.0), OverhealPercent: pointer(25.0),
	}
	if a.EventsComplete || !a.ActorAmountEventsComplete || !reflect.DeepEqual(a.Metrics, want) {
		t.Fatalf("unexpected completeness or metrics: %+v", a)
	}
	if !reflect.DeepEqual(a.Timeline, c.Events) || len(a.Abilities) != 1 || a.Abilities[0].ID != "11" ||
		a.Findings[0].Code != "DEATHS_OBSERVED" || a.Findings[1].Code != "INCOMPLETE_EVENTS" {
		t.Fatalf("actor amount stream changed sampled evidence: %+v", a)
	}
	c.ActorAmountEvents[2].Overheal = nil
	a, err = Calculate(c, "warcraftlogs")
	if err != nil || !a.ActorAmountEventsComplete || a.Metrics.HPS == nil || a.Metrics.OverhealTotal != nil ||
		a.Metrics.OverhealPercent != nil || a.Findings[2].Code != "AMOUNTS_UNAVAILABLE" {
		t.Fatalf("missing overheal hid effective healing or became zero: %+v, %v", a, err)
	}
}

func TestActorAmountStreamNeedsValidBindingAndEvidence(t *testing.T) {
	for _, tt := range []struct {
		name       string
		memberList []int
		events     []Event
		complete   bool
	}{
		{name: "unavailable stream", memberList: []int{1}},
		{name: "unknown empty participant", events: []Event{}},
		{name: "confirmed empty participant", memberList: []int{1}, events: []Event{}, complete: true},
		{name: "wrong source", memberList: []int{1}, events: []Event{{TimestampMS: 2000, Kind: "damage", SourceID: pointer(2), Amount: pointer(12.0)}}},
		{name: "outside fight", memberList: []int{1}, events: []Event{{TimestampMS: 999, Kind: "damage", SourceID: pointer(1), Amount: pointer(12.0)}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := sample()
			c.Complete = false
			c.Report.Fights[0].ParticipantIDs = tt.memberList
			c.ActorAmountEvents = tt.events
			a, err := Calculate(c, "warcraftlogs")
			if err != nil || a.ActorAmountEventsComplete != tt.complete {
				t.Fatalf("unexpected scoped coverage %+v, %v", a, err)
			}
			if tt.complete {
				if a.Metrics.DPS == nil || *a.Metrics.DPS != 0 {
					t.Fatalf("confirmed empty stream lost its zero: %+v", a.Metrics)
				}
			} else if !reflect.DeepEqual(a.Metrics, Metrics{}) {
				t.Fatalf("unconfirmed scope published metrics: %+v", a.Metrics)
			}
		})
	}
}

func TestRateOverflowAndUnknownEventsFailClosed(t *testing.T) {
	c := sample()
	c.Report.Fights[0].EndMS = 1001
	c.Events = []Event{{TimestampMS: 1000, Kind: "damage", SourceID: pointer(1), Amount: pointer(float64(maxSafe))}}
	a, _ := Calculate(c, "fixture")
	if a.Metrics.DamageTotal == nil || a.Metrics.DPS != nil {
		t.Fatal("unsafe rate exposed")
	}
	for _, kind := range []string{"unknown", "", "future_damage"} {
		c = sample()
		c.Events = []Event{{TimestampMS: 2000, Kind: kind, SourceID: pointer(1)}}
		a, _ = Calculate(c, "fixture")
		if a.EventsComplete || !reflect.DeepEqual(a.Metrics, Metrics{}) {
			t.Fatalf("unknown kind %q became zero", kind)
		}
	}
	c = sample()
	c.Events = []Event{{TimestampMS: 2000, Kind: "damage", SourceID: pointer(1), Amount: pointer(math.NaN())}}
	a, _ = Calculate(c, "fixture")
	if _, err := json.Marshal(a); err != nil || a.Metrics.DPS != nil {
		t.Fatal("invalid numeric output")
	}
}
func TestTimelineCapRetainsRecentDeathContextAndStableEvidence(t *testing.T) {
	c := sample()
	for i := 0; i < 700; i++ {
		c.Events = append(c.Events, Event{TimestampMS: 1000 + int64(i)*100, Kind: "cast", SourceID: pointer(1), AbilityID: pointer("1")})
	}
	c.Events = append(c.Events, Event{TimestampMS: 71000, Kind: "damage", SourceID: pointer(100), TargetID: pointer(1), Amount: pointer(500.0)}, Event{TimestampMS: 71100, Kind: "death", TargetID: pointer(1)})
	a, _ := Calculate(c, "fixture")
	if !a.TimelineTruncated || len(a.Timeline) != MaxTimeline || !a.EventsComplete || *a.Metrics.Casts != 700 {
		t.Fatal("timeline cap changed full-window metrics")
	}
	indexes := a.Findings[0].EventIndexes
	if a.Findings[0].Code != "DEATHS_OBSERVED" || len(indexes) != 1 || a.Timeline[indexes[0]].Kind != "death" || a.Timeline[indexes[0]-1].Kind != "damage" {
		t.Fatal("death context dropped")
	}
	for i := 1; i < len(a.Timeline); i++ {
		if a.Timeline[i].TimestampMS < a.Timeline[i-1].TimestampMS {
			t.Fatal("unordered timeline")
		}
	}
}
func TestIdentityAndWindowValidation(t *testing.T) {
	for _, code := range []string{"", "https://evil/x", "DEMOCombat00000?", "DEMOCombat000001x"} {
		if ValidCode(code) {
			t.Fatal("invalid report code accepted")
		}
	}
	c := sample()
	c.Report.Visibility = "unlisted"
	if _, err := Calculate(c, "fixture"); err == nil {
		t.Fatal("unlisted report accepted")
	}
	c = sample()
	c.ActorID = 3
	if _, err := Calculate(c, "fixture"); err == nil {
		t.Fatal("pet treated as player")
	}
	c = sample()
	c.Report.Fights[0].EndMS = 1000
	if _, err := Calculate(c, "fixture"); err == nil {
		t.Fatal("zero duration accepted")
	}
}

func TestFightMembershipPreventsFalseZerosForOtherReportPlayers(t *testing.T) {
	c := sample()
	c.Report.Fights[0].ParticipantIDs = []int{2}
	if _, err := Calculate(c, "fixture"); err == nil {
		t.Fatal("nonparticipant accepted")
	}
	c.Report.Fights[0].ParticipantIDs = nil
	a, err := Calculate(c, "fixture")
	if err != nil || a.EventsComplete || !reflect.DeepEqual(a.Metrics, Metrics{}) {
		t.Fatal("unknown participant with no observations became zero")
	}
	c.Events = []Event{{TimestampMS: 2000, Kind: "cast", SourceID: pointer(1), AbilityID: pointer("1")}}
	a, err = Calculate(c, "fixture")
	if err != nil || !a.EventsComplete || a.Metrics.DPS == nil || *a.Metrics.DPS != 0 || *a.Metrics.Casts != 1 {
		t.Fatal("observed participation not recognized")
	}
}

func TestCombatantStatsPreserveOnlySafeObservedValues(t *testing.T) {
	c := sample()
	c.Complete = false
	c.CombatantStats = &CombatantStats{ActorID: 1, ObservedAtMS: 1000, Values: map[string]float64{
		"strength": 840, "agility": 0, "mastery": 125.5,
		"stamina": -1, "haste": math.Inf(1), "versatility": math.NaN(),
		"intellect": float64(maxSafe) + 1, "unsupported": 100,
	}}

	a, err := Calculate(c, "warcraftlogs")
	if err != nil {
		t.Fatal(err)
	}
	want := &CombatantStats{ActorID: 1, ObservedAtMS: 1000, Values: map[string]float64{
		"strength": 840, "agility": 0, "mastery": 125.5,
	}}
	if !reflect.DeepEqual(a.CombatantStats, want) {
		t.Fatalf("combatant stats = %#v", a.CombatantStats)
	}
	if a.EventsComplete || !reflect.DeepEqual(a.Metrics, Metrics{}) || len(c.CombatantStats.Values) != 8 {
		t.Fatal("stats observation changed the event result or input")
	}
	body, err := json.Marshal(a.CombatantStats)
	if err != nil || string(body) != `{"observed_at_ms":1000,"values":{"agility":0,"mastery":125.5,"strength":840}}` {
		t.Fatalf("unexpected stats payload %s (%v)", body, err)
	}
}

func TestCombatantStatsMustMatchSelectedActorAndFightWindow(t *testing.T) {
	valid := map[string]float64{"strength": 1}
	for _, tt := range []struct {
		name  string
		stats *CombatantStats
		known bool
	}{
		{"legacy absence", nil, false},
		{"other actor", &CombatantStats{ActorID: 2, ObservedAtMS: 1000, Values: valid}, false},
		{"before fight", &CombatantStats{ActorID: 1, ObservedAtMS: 999, Values: valid}, false},
		{"after fight", &CombatantStats{ActorID: 1, ObservedAtMS: 121001, Values: valid}, false},
		{"empty values", &CombatantStats{ActorID: 1, ObservedAtMS: 1000}, false},
		{"invalid values", &CombatantStats{ActorID: 1, ObservedAtMS: 1000, Values: map[string]float64{"mastery": -1}}, false},
		{"fight end", &CombatantStats{ActorID: 1, ObservedAtMS: 121000, Values: valid}, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := sample()
			c.CombatantStats = tt.stats
			a, err := Calculate(c, "fixture")
			if err != nil {
				t.Fatal(err)
			}
			if (a.CombatantStats != nil) != tt.known || !a.EventsComplete || a.Metrics.DPS == nil || *a.Metrics.DPS != 0 {
				t.Fatalf("unexpected analysis with stats %+v", a.CombatantStats)
			}
			body, err := json.Marshal(a)
			if err != nil || strings.Contains(string(body), `"combatant_stats"`) != tt.known {
				t.Fatalf("unexpected analysis payload %s (%v)", body, err)
			}
		})
	}
}
