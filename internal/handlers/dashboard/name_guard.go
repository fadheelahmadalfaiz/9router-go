package dashboard

import (
	"net/http"
	"strings"

	"9router/proxy/internal/handlerutil"
)

// nameNamespace labels one of the three user-editable spaces a bare model
// string can be written into from the dashboard.
const (
	nsCombo       = "combo"
	nsModelAlias  = "modelAlias"
	nsCustomModel = "customModel"
)

// guardNameCollision refuses a write whose name is already taken in one of the
// other two spaces.
//
// A bare model string the client sends is resolved against a model alias first
// (resolveModel step 2), then a combo name (step 3), then a provider node
// prefix. The alias-vs-combo pair is therefore a real shadow and stays refused.
// The custom model is a different case: it lives at "<prefix>/<id>", so it does
// not share an address with the bare name and is allowed to coexist with an
// alias — see customModelAndAliasMayCoexist. Only the /v1/models listing then
// carries both "<id>" and "<prefix>/<id>" side by side, which is a naming
// ambiguity to be aware of, not a target that can no longer be reached.
//
// Comparison is exact after trimming, matching resolution itself: resolveModel
// looks combos up with `WHERE name = ?` and aliases by exact kv key, so
// "Combo" and "combo" are two genuinely different addresses and the guard must
// not refuse either. It guards the write only, so rows that already collide
// keep working — nothing is migrated or hidden.
//
// customModelAndAliasMayCoexist reports whether a custom model id and a model
// alias may hold the same bare name.
//
// They may, and did not need to be blocked. A custom model is stored as
// "<providerAlias>|<id>" and is addressed "<prefix>/<id>" — a different address
// from the bare "id" the alias key owns. Resolution confirms it: a bare request
// takes the alias at step 2 (resolveModel), and a prefixed one never reaches
// the bare-name alias table at all, because resolvePrefixProvider matches the
// prefix. Neither one hides the other.
//
// The pair that genuinely shadows is alias vs combo, and it stays refused: a
// combo is addressed by its bare name only, so an alias of that name makes the
// combo unreachable. The custom-model check below is kept for combos for the
// /v1/models ambiguity reason the original comment described, which is a real
// but much weaker concern than shadowing.
func customModelAndAliasMayCoexist(namespace string) bool {
	return namespace == nsCustomModel || namespace == nsModelAlias
}

// Returns true when it has already written the 409 and the caller must return.
func (h *DashboardHandler) guardNameCollision(w http.ResponseWriter, namespace, name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}

	if namespace != nsCombo {
		if combo, err := h.Repo.GetComboByName(name); err == nil && combo != nil {
			writeNameConflict(w, "COMBO_NAME_CONFLICT", name,
				"a combo named \""+name+"\" already answers to this name; a bare request for \""+name+
					"\" is the combo, so rename the "+namespaceLabel(namespace)+" or the combo so the name addresses one target")
			return true
		}
	}

	if namespace != nsModelAlias && !customModelAndAliasMayCoexist(namespace) {
		if target, err := h.Repo.GetModelAlias(name); err == nil && target != "" {
			writeNameConflict(w, "MODEL_ALIAS_CONFLICT", name,
				"a model alias \""+name+"\" already resolves this name, and an alias is consulted before a combo, "+
					"so a combo named \""+name+"\" would be unreachable; rename the "+namespaceLabel(namespace)+
					" or the alias so the name addresses one target")
			return true
		}
	}

	if namespace != nsCustomModel && !customModelAndAliasMayCoexist(namespace) {
		if owner, ok := customModelOwner(h, name); ok {
			writeNameConflict(w, "CUSTOM_MODEL_NAME_CONFLICT", name,
				"the custom model \""+owner+"/"+name+"\" already uses this id; rename one of them so the name addresses one target")
			return true
		}
	}

	return false
}

// namespaceLabel names the space the caller is writing into, so a conflict
// message can point at what the user was actually saving.
func namespaceLabel(namespace string) string {
	switch namespace {
	case nsCustomModel:
		return "custom model"
	case nsModelAlias:
		return "model alias"
	default:
		return "entry"
	}
}

// customModelOwner returns the provider alias of a custom model carrying id,
// reporting false when no custom model uses it. Only the id is compared: two
// nodes may both expose "glm-5.3" as <prefix>/glm-5.3 without either shadowing
// the other.
func customModelOwner(h *DashboardHandler, id string) (string, bool) {
	customs, err := h.Repo.GetCustomModels()
	if err != nil {
		return "", false
	}
	for _, cm := range customs {
		if cm != nil && cm.ID == id {
			return cm.ProviderAlias, true
		}
	}
	return "", false
}

// writeNameConflict emits the typed 409 the connection-name guard established
// (PROVIDER_NAME_CONFLICT), so clients already handling that shape can handle
// this one too.
func writeNameConflict(w http.ResponseWriter, code, name, detail string) {
	handlerutil.WriteJSON(w, http.StatusConflict, map[string]any{
		"error":   detail,
		"code":    code,
		"name":    name,
		"details": detail,
	})
}
