package goaspen

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"path"
	"path/filepath"
	"strings"
	"text/template"
)

const (
	SimplateTypeRendered   = "rendered"
	SimplateTypeStatic     = "static"
	SimplateTypeNegotiated = "negotiated"
	SimplateTypeJson       = "json"
)

var (
	SimplateTypes = []string{
		SimplateTypeJson,
		SimplateTypeNegotiated,
		SimplateTypeRendered,
		SimplateTypeStatic,
	}
	simplateTypeTemplates = map[string]*template.Template{
		SimplateTypeJson:       escapedSimplateTemplate(simplateTypeJSONTmpl, "goaspen-gen-json"),
		SimplateTypeRendered:   escapedSimplateTemplate(simplateTypeRenderedTmpl, "goaspen-gen-rendered"),
		SimplateTypeNegotiated: escapedSimplateTemplate(simplateTypeNegotiatedTmpl, "goaspen-gen-negotiated"),
		SimplateTypeStatic:     nil,
	}
	defaultRenderer = "#!go/text/template"
)

type Simplate struct {
	SiteRoot      string          `json:"-"`
	Filename      string          `json:"-"`
	Type          string          `json:"type"`
	ContentType   string          `json:"content_type"`
	InitPage      *SimplatePage   `json:"-"`
	LogicPage     *SimplatePage   `json:"-"`
	TemplatePages []*SimplatePage `json:"-"`
}

type SimplatePage struct {
	Parent *Simplate
	Body   string
	Spec   *SimplatePageSpec
}

type SimplatePageSpec struct {
	ContentType string
	Renderer    string
}

func NewSimplateFromString(siteRoot, filename, content string) (*Simplate, error) {
	var err error

	filename, err = filepath.Abs(filename)
	if err != nil {
		return nil, err
	}

	filename, err = filepath.Rel(siteRoot, filename)
	if err != nil {
		return nil, err
	}

	rawPages := strings.Split(content, "")
	nbreaks := len(rawPages) - 1

	s := &Simplate{
		SiteRoot:    siteRoot,
		Filename:    filename,
		Type:        SimplateTypeStatic,
		ContentType: mime.TypeByExtension(path.Ext(filename)),
	}

	if nbreaks == 1 || nbreaks == 2 {
		s.InitPage, err = NewSimplatePage(s, rawPages[0], false)
		if err != nil {
			return nil, err
		}

		s.LogicPage, err = NewSimplatePage(s, rawPages[1], false)
		if err != nil {
			return nil, err
		}

		if s.ContentType == "application/json" {
			s.Type = SimplateTypeJson
		} else {
			s.Type = SimplateTypeRendered
			templatePage, err := NewSimplatePage(s, rawPages[2], true)
			if err != nil {
				return nil, err
			}

			s.TemplatePages = append(s.TemplatePages, templatePage)
		}

		return s, nil
	}

	if nbreaks > 2 {
		s.Type = SimplateTypeNegotiated
		s.InitPage, err = NewSimplatePage(s, rawPages[0], false)
		if err != nil {
			return nil, err
		}

		s.LogicPage, err = NewSimplatePage(s, rawPages[1], false)
		if err != nil {
			return nil, err
		}

		for _, rawPage := range rawPages[2:] {
			templatePage, err := NewSimplatePage(s, rawPage, true)
			if err != nil {
				return nil, err
			}

			s.TemplatePages = append(s.TemplatePages, templatePage)
		}

		return s, nil
	}

	return s, nil
}

func (me *Simplate) FirstTemplatePage() *SimplatePage {
	if len(me.TemplatePages) > 0 {
		return me.TemplatePages[0]
	}

	return nil
}

func (me *Simplate) Execute(wr io.Writer) (err error) {
	errAddr := &err

	defer func(err *error) {
		r := recover()
		if r != nil {
			*err = errors.New(fmt.Sprintf("%v", r))
		}
	}(errAddr)

	debugf("Executing to %s\n", wr)
	*errAddr = simplateTypeTemplates[me.Type].Execute(wr, me)
	return
}

func (me *Simplate) escapedFilename() string {
	fn := filepath.Clean(me.Filename)
	lessDots := strings.Replace(fn, ".", "-DOT-", -1)
	lessSlashes := strings.Replace(lessDots, "/", "-SLASH-", -1)
	return strings.Replace(lessSlashes, " ", "-SPACE-", -1)
}

func (me *Simplate) OutputName() string {
	if me.Type == SimplateTypeStatic {
		return me.Filename
	}

	return me.escapedFilename() + ".go"
}

func (me *Simplate) FuncName() string {
	escaped := me.escapedFilename()
	parts := strings.Split(escaped, "-")
	for i, part := range parts {
		var capitalized []string
		capitalized = append(capitalized, strings.ToUpper(string(part[0])))
		capitalized = append(capitalized, strings.ToLower(part[1:]))
		parts[i] = strings.Join(capitalized, "")
	}

	return strings.Join(parts, "")
}

func (me *Simplate) ConstName() string {
	escaped := me.escapedFilename()
	uppered := strings.ToUpper(escaped)
	return strings.Replace(uppered, "-", "_", -1)
}

func NewSimplatePageSpec(simplate *Simplate, specline string) (*SimplatePageSpec, error) {
	sps := &SimplatePageSpec{
		ContentType: simplate.ContentType,
		Renderer:    defaultRenderer,
	}

	switch simplate.Type {
	case SimplateTypeStatic:
		return &SimplatePageSpec{}, nil
	case SimplateTypeJson:
		return sps, nil
	case SimplateTypeRendered:
		renderer := specline
		if len(renderer) < 1 {
			renderer = defaultRenderer
		}

		sps.Renderer = renderer
		return sps, nil
	case SimplateTypeNegotiated:
		parts := strings.Fields(specline)
		nParts := len(parts)

		if nParts < 1 || nParts > 2 {
			return nil, errors.New(fmt.Sprintf("A negotiated resource specline "+
				"must have one or two parts: #!renderer media/type. Yours is %q",
				specline))
		}

		if nParts == 1 {
			sps.ContentType = parts[0]
			sps.Renderer = defaultRenderer
			return sps, nil
		} else {
			sps.ContentType = parts[0]
			sps.Renderer = parts[1]
			return sps, nil
		}
	}

	return nil, errors.New(fmt.Sprintf("Can't make a page spec "+
		"for simplate type %q", simplate.Type))
}

func NewSimplatePage(simplate *Simplate, rawPage string, needsSpec bool) (*SimplatePage, error) {
	spec := &SimplatePageSpec{}
	var err error

	specline := ""
	body := rawPage

	if needsSpec {
		parts := strings.SplitN(rawPage, "\n", 2)
		specline = parts[0]
		body = parts[1]

		spec, err = NewSimplatePageSpec(simplate,
			strings.TrimSpace(strings.Replace(specline, "", "", -1)))
		if err != nil {
			return nil, err
		}
	}

	sp := &SimplatePage{
		Parent: simplate,
		Body:   body,
		Spec:   spec,
	}
	return sp, nil
}
