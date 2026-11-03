package httptransport

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"kipet-wow-analyzer/internal/application"
	"kipet-wow-analyzer/internal/domain/character"
	"kipet-wow-analyzer/internal/domain/guild"
)

func (h *Handler) guild(w http.ResponseWriter, r *http.Request, section string) {
	locale, err := localeOf(r)
	region, a := pathParam(r, "region")
	realm, b := pathParam(r, "realm")
	slug, c := pathParam(r, "name")
	if err != nil || a != nil || b != nil || c != nil {
		h.fail(w, r, character.NewFault("INVALID_ARGUMENT", false), 400)
		return
	}
	key, err := guild.NormalizeKey(region, realm, slug)
	if err != nil {
		h.fail(w, r, err, 400)
		return
	}
	if h.Guild == nil {
		h.fail(w, r, character.NewFault("INFRASTRUCTURE_UNAVAILABLE", true), 503)
		return
	}
	var result application.Result
	switch section {
	case "profile":
		result, err = h.Guild.ReadProfile(r.Context(), key, locale)
	case "roster":
		result, err = h.Guild.ReadRoster(r.Context(), key, locale)
	case "analysis":
		result, err = h.Guild.ReadAnalysis(r.Context(), key, locale)
	}
	if err != nil {
		h.fail(w, r, err, 0)
		return
	}
	h.result(w, r, result)
}
func (h *Handler) socialBody(w http.ResponseWriter, r *http.Request, out any) bool {
	if r.Header.Get("Content-Type") != "application/json" && r.Header.Get("Content-Type") != "application/json; charset=utf-8" {
		h.fail(w, r, character.NewFault("UNSUPPORTED_MEDIA_TYPE", false), 415)
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8192)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		var big *http.MaxBytesError
		if errors.As(err, &big) {
			h.fail(w, r, character.NewFault("REQUEST_TOO_LARGE", false), 413)
		} else {
			h.fail(w, r, character.NewFault("INVALID_ARGUMENT", false), 400)
		}
		return false
	}
	if dec.Decode(&struct{}{}) != io.EOF {
		h.fail(w, r, character.NewFault("INVALID_ARGUMENT", false), 400)
		return false
	}
	return true
}
func (h *Handler) refreshGuild(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Region string `json:"region"`
		Realm  string `json:"realm"`
		Name   string `json:"name"`
		Locale string `json:"locale"`
	}
	if !h.socialBody(w, r, &body) {
		return
	}
	if body.Locale == "" {
		body.Locale = "ru-RU"
	}
	if body.Locale != "ru-RU" && body.Locale != "en-US" {
		h.fail(w, r, character.NewFault("INVALID_ARGUMENT", false), 400)
		return
	}
	key, err := guild.NormalizeKey(body.Region, body.Realm, body.Name)
	if err != nil {
		h.fail(w, r, err, 400)
		return
	}
	if h.Guild == nil {
		h.fail(w, r, character.NewFault("INFRASTRUCTURE_UNAVAILABLE", true), 503)
		return
	}
	result, err := h.Guild.Refresh(r.Context(), key, body.Locale)
	if err != nil {
		h.fail(w, r, err, 0)
		return
	}
	h.result(w, r, result)
}
func (h *Handler) compare(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Characters []guild.CharacterKey `json:"characters"`
		Locale     string               `json:"locale"`
	}
	if !h.socialBody(w, r, &body) {
		return
	}
	if body.Locale == "" {
		body.Locale = "ru-RU"
	}
	if h.Comparison == nil {
		h.fail(w, r, character.NewFault("INFRASTRUCTURE_UNAVAILABLE", true), 503)
		return
	}
	result, err := h.Comparison.Read(r.Context(), body.Characters, body.Locale)
	if err != nil {
		h.fail(w, r, err, 0)
		return
	}
	h.result(w, r, result)
}
