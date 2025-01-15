package server

import (
	"fmt"
	"html/template"
	"strings"

	"github.com/rs/zerolog/log"
)

type TemplateFinder interface {
	Find(templateName string) (*template.Template, error)
}

func trim(arg string) string {
	return strings.Join(strings.Split(arg, " "), "")
}

func unsescapeHTML(s string) template.HTML {
	return template.HTML(s)
}

type dirsFinder struct {
	dirs []string
}

func NewDirsFinder(dirs ...string) *dirsFinder {
	return &dirsFinder{dirs}
}

func (finder *dirsFinder) Find(templateName string) (*template.Template, error) {
	tmplt := template.New(templateName).Funcs(template.FuncMap{
		"trim":         trim,
		"unescapeHTML": unsescapeHTML,
	})
	var err error

	for _, dir := range finder.dirs {
		glob := fmt.Sprintf("%s/*.go.html", dir)

		tmplt, err = tmplt.ParseGlob(glob)
		if err != nil {
			log.Error().Err(err).Str("glob", glob).Msg("Failed to parse glob")

			return nil, err
		}
	}

	return tmplt, nil
}