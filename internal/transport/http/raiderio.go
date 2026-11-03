package httptransport

import (
	"net/http"

	"kipet-wow-analyzer/internal/domain/character"
	"kipet-wow-analyzer/internal/domain/raiderio"
)

type raiderKeyNormalizer func(region, realm, name string) (raiderio.Key, error)

func (h *Handler) readRaider(normalize raiderKeyNormalizer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		locale, localeErr := localeOf(r)
		region, regionErr := pathParam(r, "region")
		realm, realmErr := pathParam(r, "realm")
		name, nameErr := pathParam(r, "name")
		if localeErr != nil || regionErr != nil || realmErr != nil || nameErr != nil {
			h.fail(w, r, character.NewFault("INVALID_ARGUMENT", false), 400)
			return
		}
		key, err := normalize(region, realm, name)
		if err != nil {
			h.fail(w, r, err, 400)
			return
		}
		if h.Raider == nil {
			h.fail(w, r, character.NewFault("RAIDERIO_NOT_CONFIGURED", false), 503)
			return
		}
		result, err := h.Raider.Read(r.Context(), key, locale)
		if err != nil {
			h.fail(w, r, err, 0)
			return
		}
		h.result(w, r, result)
	}
}

func (h *Handler) refreshRaider(normalize raiderKeyNormalizer) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			raiderio.Key
			Locale string `json:"locale"`
		}
		if !h.socialBody(w, r, &body) {
			return
		}
		if body.Locale == "" {
			body.Locale = "ru-RU"
		}
		key, err := normalize(body.Region, body.Realm, body.Name)
		if err != nil || body.Locale != "ru-RU" && body.Locale != "en-US" {
			h.fail(w, r, character.NewFault("INVALID_ARGUMENT", false), 400)
			return
		}
		if h.Raider == nil {
			h.fail(w, r, character.NewFault("RAIDERIO_NOT_CONFIGURED", false), 503)
			return
		}
		result, err := h.Raider.Refresh(r.Context(), key, body.Locale)
		if err != nil {
			h.fail(w, r, err, 0)
			return
		}
		h.result(w, r, result)
	}
}
