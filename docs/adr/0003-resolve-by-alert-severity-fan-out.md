# Resolve previous severities by sending every Alert severity

The `severity` label is part of an Alert's identity, so when an Alarm is cleared or changes severity, its new Alert does not resolve the Alert for its previous severity, which stayed firing until the resolve timeout plus the Resolve hold passed. The Alarm does not carry its previous severity and the receiver keeps no record of the Alerts it has sent (ADR 0002), so a Cleared Alarm is sent as a resolved Alert for every distinct Alert severity, and a firing Alarm whose Last update time is within the cleared Alarm window is also sent as a resolved Alert for every other Alert severity. Alertmanager does not notify for an alert group that has only ever held resolved Alerts, so only the Alert that was firing produces a notification.

## Considered Options

- **Moving severity to an annotation**: removes it from the identity, but breaks every Alertmanager route, inhibit rule and silence that matches on `severity`.
- **Remembering the last Alert severity of each Alarm (stateful)**: sends one resolved Alert instead of up to five, but needs memory for every Alarm keyed on instance name and ID, and the state is lost if the receiver restarts, which is when Cleared Alarms arrive in bulk with the first Snapshot.
- **Documenting the delay**: leaves clears taking up to the resolve timeout plus the hold, and duplicate firing Alerts during escalation.

## Consequences

A Cleared Alarm, or a firing Alarm updated within the window, is sent as up to five Alerts on every Alarm update and Snapshot while it is in the window. Repeated events that do not change severity also update the Last update time, so their resolved Alerts are sent too; they are harmless as only one Alert severity is ever current. No Alert is sent with a `severity` of `cleared`.
