package server

import (
	"fmt"
	"io"
	"maps"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"text/template"

	pb "github.com/andrewheberle/onms-grpc-receiver/pkg/spog"
)

// templateFuncs are the functions available to the runbook URL template and
// label templates. Where Alertmanager's notification templates have a
// function of the same purpose, its name is used.
var templateFuncs = template.FuncMap{
	"pathEscape":   url.PathEscape,
	"toLower":      strings.ToLower,
	"toUpper":      strings.ToUpper,
	"trimSpace":    strings.TrimSpace,
	"hasPrefix":    strings.HasPrefix,
	"hasSuffix":    strings.HasSuffix,
	"contains":     strings.Contains,
	"match":        regexp.MatchString,
	"reReplaceAll": reReplaceAll,
}

// reReplaceAll replaces the matches of the regular expression pattern in text
// with repl, which may refer to submatches as $1 and so on
func reReplaceAll(pattern, repl, text string) (string, error) {
	re, err := regexp.Compile(pattern)
	if err != nil {
		return "", err
	}

	return re.ReplaceAllString(text, repl), nil
}

// labelTemplateData is what a label template is executed with: the labels of
// the alert, and the details of its alarm that do not change while the alarm
// is active. Details that change, such as its count or acknowledgement, are
// left out, as a label built from them would make the alert a new alert each
// time they changed. See ADR 0007.
type labelTemplateData struct {
	Labels map[string]string
	Alarm  templateAlarm
}

// templateAlarm is the details of an alarm a label template can use
type templateAlarm struct {
	ID                    uint64
	UEI                   string
	NodeID                uint64
	NodeLabel             string
	ForeignSource         string
	ForeignID             string
	Location              string
	IPAddress             string
	ServiceName           string
	ReductionKey          string
	ClearKey              string
	IfIndex               uint32
	ManagedObjectType     string
	ManagedObjectInstance string
}

// newTemplateAlarm returns the details of an alarm a label template can use
func newTemplateAlarm(alarm *pb.Alarm) templateAlarm {
	return templateAlarm{
		ID:                    alarm.GetId(),
		UEI:                   alarm.GetUei(),
		NodeID:                alarm.GetNodeCriteria().GetId(),
		NodeLabel:             alarm.GetNodeCriteria().GetNodeLabel(),
		ForeignSource:         alarm.GetNodeCriteria().GetForeignSource(),
		ForeignID:             alarm.GetNodeCriteria().GetForeignId(),
		Location:              alarm.GetNodeCriteria().GetLocation(),
		IPAddress:             alarm.GetIpAddress(),
		ServiceName:           alarm.GetServiceName(),
		ReductionKey:          alarm.GetReductionKey(),
		ClearKey:              alarm.GetClearKey(),
		IfIndex:               alarm.GetIfIndex(),
		ManagedObjectType:     alarm.GetManagedObjectType(),
		ManagedObjectInstance: alarm.GetManagedObjectInstance(),
	}
}

// labelTemplate is a parsed label template and the name of the label it sets
type labelTemplate struct {
	name string
	tmpl *template.Template
}

// parseLabelTemplates parses label templates keyed by label name, returning
// them sorted by name, or an error describing every problem found. A name
// follows the same rules as an extra label, and may not be the name of a
// built-in or extra label. Each template is executed once so mistakes such as
// an unknown field are found at startup rather than for the first alarm.
func parseLabelTemplates(m, extraLabels map[string]string) ([]labelTemplate, error) {
	templates := make([]labelTemplate, 0, len(m))

	// executed with the labels every alarm alert has and an empty alarm
	sample := labelTemplateData{Labels: map[string]string{
		"alertname":     "uei.opennms.org/nodes/nodeDown",
		"alarm_id":      "1",
		"node_name":     "node",
		"instance_name": "OpenNMS",
		"severity":      "major",
	}}
	maps.Copy(sample.Labels, extraLabels)

	var problems []string
	for _, name := range slices.Sorted(maps.Keys(m)) {
		switch {
		case slices.Contains(builtinLabels, name):
			problems = append(problems, fmt.Sprintf("%q clashes with a built-in label", name))
			continue
		case !validName.MatchString(name):
			problems = append(problems, fmt.Sprintf("%q is not a valid name", name))
			continue
		case strings.HasPrefix(name, "__"):
			problems = append(problems, fmt.Sprintf("%q is reserved as it starts with \"__\"", name))
			continue
		case m[name] == "":
			problems = append(problems, fmt.Sprintf("%q has an empty template", name))
			continue
		}
		if _, ok := extraLabels[name]; ok {
			problems = append(problems, fmt.Sprintf("%q clashes with an extra label", name))
			continue
		}

		tmpl, err := template.New(name).
			Option("missingkey=zero").
			Funcs(templateFuncs).
			Parse(m[name])
		if err == nil {
			err = tmpl.Execute(io.Discard, sample)
		}
		if err != nil {
			problems = append(problems, fmt.Sprintf("%q: %v", name, err))
			continue
		}

		templates = append(templates, labelTemplate{name: name, tmpl: tmpl})
	}

	if len(problems) > 0 {
		return nil, fmt.Errorf("invalid label templates: %s", strings.Join(problems, "; "))
	}

	return templates, nil
}

// addTemplateLabels adds the label of each label template to labels, executed
// with labels as they are before any are added. A template that fails or
// gives an empty value adds no label, and a failure is logged.
func (t *alertTranslator) addTemplateLabels(labels map[string]string, alarm templateAlarm) {
	if len(t.parsedLabelTemplates) == 0 {
		return
	}

	data := labelTemplateData{Labels: maps.Clone(labels), Alarm: alarm}
	for _, lt := range t.parsedLabelTemplates {
		var b strings.Builder
		if err := lt.tmpl.Execute(&b, data); err != nil {
			t.logger.Warn("problem executing label template", "label", lt.name, "alarm_id", alarm.ID, "error", err)
			continue
		}

		if v := b.String(); v != "" {
			labels[lt.name] = v
		}
	}
}
