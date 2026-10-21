package server

import (
	"fmt"
	"html"
	"maps"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"

	pb "github.com/andrewheberle/onms-grpc-receiver/pkg/spog"
	"github.com/go-openapi/strfmt"
	"github.com/prometheus/alertmanager/api/v2/models"
)

// clearedAlarmWindow is how long after its last update a cleared alarm is
// still sent. Older cleared alarms are included in every snapshot and have
// already been resolved.
const clearedAlarmWindow = time.Minute * 5

var (
	// builtinLabels are the labels the receiver may set on alerts itself
	builtinLabels = []string{
		"alertname",
		"alarm_id",
		"node_name",
		"foreign_source",
		"instance_name",
		"severity",
		"service",
		"ip_address",
		"site",
	}

	// builtinAnnotations are the annotations the receiver may set on alerts
	// itself
	builtinAnnotations = []string{
		"summary",
		"description",
		"runbook_url",
		"operator_instructions",
	}
)

// alertTranslator turns alarms and heartbeats from Horizon instances into
// alerts for Alertmanager
type alertTranslator struct {
	// labels and annotations added to all alerts
	extraLabels      map[string]string
	extraAnnotations map[string]string

	// maps instance ID's to the base URL of the Horizon instance
	urlMap map[string]string

	resolveTimeout time.Duration
}

// validate returns an error if any extra label or annotation clashes with a
// built-in one, has an invalid name or has an empty value
func (t *alertTranslator) validate() error {
	if err := validateAlertFields("label", t.extraLabels, builtinLabels); err != nil {
		return err
	}

	return validateAlertFields("annotation", t.extraAnnotations, builtinAnnotations)
}

var validName = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// validateAlertFields checks labels or annotations to be added to all alerts,
// returning a single error describing every problem found
func validateAlertFields(kind string, fields map[string]string, builtin []string) error {
	var problems []string
	for _, name := range slices.Sorted(maps.Keys(fields)) {
		switch {
		case slices.Contains(builtin, name):
			problems = append(problems, fmt.Sprintf("%q clashes with a built-in %s", name, kind))
		case !validName.MatchString(name):
			problems = append(problems, fmt.Sprintf("%q is not a valid name", name))
		case strings.HasPrefix(name, "__"):
			problems = append(problems, fmt.Sprintf("%q is reserved as it starts with \"__\"", name))
		case fields[name] == "":
			problems = append(problems, fmt.Sprintf("%q has an empty value", name))
		}
	}

	if len(problems) > 0 {
		return fmt.Errorf("invalid alert %ss: %s", kind, strings.Join(problems, "; "))
	}

	return nil
}

// alarm returns the alert for an alarm, or nil if the alarm should not be
// sent
func (t *alertTranslator) alarm(ia instanceAlarm) (*models.PostableAlert, error) {
	alarm := ia.alarm

	// ignore Normal severity alarms
	if alarm.GetSeverity() == uint32(pb.Severity_NORMAL) {
		return nil, nil
	}

	firstEventTime := time.UnixMilli(int64(alarm.GetFirstEventTime()))

	// The last update time is the later of the last event time and the last
	// automation time. It is used as when an alarm was cleared, as a clear
	// event reduced into the alarm changes its last event time, but a clear
	// by automation (eg. a Drools rule) only changes its last automation time.
	lastUpdateTime := time.UnixMilli(int64(alarm.GetLastUpdateTime()))

	// skip alarms that were cleared too long ago. Firing alarms are always
	// sent regardless of age, as snapshots are what keep long-running alarms
	// active in alertmanager.
	if alarm.GetSeverity() == uint32(pb.Severity_CLEARED) && lastUpdateTime.Before(ia.now.Add(-clearedAlarmWindow)) {
		return nil, nil
	}

	// add basics
	labels := map[string]string{
		"alertname":     alarm.GetUei(),
		"alarm_id":      fmt.Sprint(alarm.GetId()),
		"node_name":     alarm.GetNodeCriteria().GetNodeLabel(),
		"instance_name": ia.instanceName,
		"severity":      strings.ToLower(pb.Severity_name[int32(alarm.GetSeverity())]),
	}

	// add service if set
	if service := alarm.GetServiceName(); service != "" {
		labels["service"] = service
	}

	// add ip_address if set
	if ip := alarm.GetIpAddress(); ip != "" {
		labels["ip_address"] = ip
	}

	// set site as node location or site if mapping set
	if location := alarm.GetNodeCriteria().GetLocation(); location != "" {
		labels["site"] = location
	}

	// add foreign_source (requisition) if set
	if fs := alarm.GetNodeCriteria().GetForeignSource(); fs != "" {
		labels["foreign_source"] = fs
	}

	// add human readable details as annotations so they are not part of
	// the identity of the alert
	annotations := make(map[string]string)
	if summary := plainText(alarm.GetLogMessage()); summary != "" {
		annotations["summary"] = summary
	}
	if description := plainText(alarm.GetDescription()); description != "" {
		annotations["description"] = description
	}
	runbook, instructions := operatorInstructions(alarm.GetOperatorInstructions())
	if runbook != "" {
		annotations["runbook_url"] = runbook
	}
	if instructions != "" {
		annotations["operator_instructions"] = instructions
	}

	// add configured labels and annotations
	maps.Copy(labels, t.extraLabels)
	maps.Copy(annotations, t.extraAnnotations)

	alert := models.Alert{
		Labels: labels,
	}

	// add generator URL if mapping set
	if baseUrl := inmap(ia.instanceID, t.urlMap); baseUrl != "" {
		u, err := url.JoinPath(baseUrl, "/alarm/detail.htm")
		if err != nil {
			return nil, fmt.Errorf("problem creating generatorURL: %w", err)
		}
		alert.GeneratorURL = strfmt.URI(u + fmt.Sprintf("?id=%d", alarm.GetId()))
	}

	// default start and end time based on first event time and when the
	// alarm was received plus the resolve timeout
	post := &models.PostableAlert{
		Alert:       alert,
		Annotations: annotations,
		StartsAt:    strfmt.DateTime(firstEventTime),
		EndsAt:      strfmt.DateTime(ia.now.Add(t.resolveTimeout)),
	}

	// end cleared alerts when the alarm was cleared
	if alarm.GetSeverity() == uint32(pb.Severity_CLEARED) {
		post.EndsAt = strfmt.DateTime(lastUpdateTime)
	}

	return post, nil
}

// heartbeat returns the alert for a heartbeat received at now
func (t *alertTranslator) heartbeat(instanceName string, now time.Time) *models.PostableAlert {
	labels := map[string]string{
		"alertname":     "OpenNMSHeartbeat",
		"instance_name": instanceName,
	}
	maps.Copy(labels, t.extraLabels)

	annotations := make(map[string]string)
	maps.Copy(annotations, t.extraAnnotations)

	return &models.PostableAlert{
		Alert: models.Alert{
			Labels: labels,
		},
		Annotations: annotations,
		StartsAt:    strfmt.DateTime(now),
		EndsAt:      strfmt.DateTime(now.Add(t.resolveTimeout)),
	}
}

var htmlTag = regexp.MustCompile(`<[^>]*>`)

// plainText converts the HTML used in OpenNMS alarm fields to plain text on
// a single line
func plainText(s string) string {
	s = htmlTag.ReplaceAllString(s, " ")

	return strings.Join(strings.Fields(html.UnescapeString(s)), " ")
}

var (
	htmlLink = regexp.MustCompile(`(?i)<a\s[^>]*href\s*=\s*["']([^"']+)["']`)
	plainURL = regexp.MustCompile(`(?i)https?://[^\s<>"']+`)
)

// operatorInstructions splits the operator instructions of an alarm into the
// first http(s) URL they contain, for use as a runbook URL, and their plain
// text. The text is empty when the instructions were only the URL.
func operatorInstructions(s string) (runbook, text string) {
	text = plainText(s)

	// prefer a link in the HTML, as its URL is lost from the plain text
	for _, m := range htmlLink.FindAllStringSubmatch(s, -1) {
		if u := strings.TrimSpace(html.UnescapeString(m[1])); plainURL.FindString(u) == u {
			runbook = u
			break
		}
	}

	if runbook == "" {
		runbook = trimURL(plainURL.FindString(text))
	}

	if text == runbook {
		text = ""
	}

	return runbook, text
}

// trimURL removes trailing punctuation that is likely part of the sentence
// containing a URL rather than the URL itself
func trimURL(u string) string {
	for {
		trimmed := strings.TrimRight(u, ".,;:!?")
		if strings.HasSuffix(trimmed, ")") && strings.Count(trimmed, "(") < strings.Count(trimmed, ")") {
			trimmed = strings.TrimSuffix(trimmed, ")")
		}
		if trimmed == u {
			return u
		}
		u = trimmed
	}
}

func inmap(k string, m map[string]string) string {
	if v, ok := m[k]; ok {
		return v
	}

	return ""
}
