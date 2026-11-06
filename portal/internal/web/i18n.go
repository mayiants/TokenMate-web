package web

import (
	"encoding/json"
	"fmt"

	"github.com/ai-proxy/ai-portal-go/pkg/i18n"
	"github.com/ai-proxy/ai-portal-go/web"
)

// LoadTranslations loads i18n JSON files from embedded FS.
// zh.json now contains both legacy Chinese keys and the English UI-chrome
// keys introduced by the Plan B refactor, so no extra overlay is needed.
func LoadTranslations() (i18n.I18n, error) {
	i := make(i18n.I18n)

	for _, lang := range []string{"zh", "en"} {
		data, err := web.FS.ReadFile(fmt.Sprintf("i18n/%s.json", lang))
		if err != nil {
			return nil, fmt.Errorf("read i18n %s: %w", lang, err)
		}
		var dict map[string]string
		if err := json.Unmarshal(data, &dict); err != nil {
			return nil, fmt.Errorf("parse i18n %s: %w", lang, err)
		}
		i[lang] = dict
	}

	return i, nil
}
