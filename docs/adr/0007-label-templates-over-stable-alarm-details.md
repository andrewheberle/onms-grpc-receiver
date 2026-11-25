# Label templates over a fixed set of stable Alarm details

Operators need labels beyond the Built-in labels and static Extra labels to group and route Alerts in Alertmanager, such as a device class so switch, VM and access point outages land in different alert groups. Alertmanager only groups, routes, silences and inhibits on labels, and labels are an Alert's identity. So operators define Label templates (`--alert.label-templates`), Go templates whose values become labels on Alarm Alerts. A template is given the Alert's labels and a fixed set of Alarm details that do not change while the Alarm is active: IDs, UEI, node, location, interface, service, reduction and clear keys, and managed object. Volatile details such as the count, acknowledgement, times, description and log message are left out, as a label built from them would make the Alert a new Alert each time they changed. An empty result adds no label, a template that fails for an Alarm is logged and adds no label, and every template is parsed and executed once at startup so mistakes stop the receiver. Templates use the same functions as the Runbook URL template, named after Alertmanager's notification template functions where it has an equivalent.

## Considered Options

- **Sending every Alarm field as a label**: the volatile fields would give an Alert a new identity on every Alarm update, causing a stream of firing and resolved notifications.
- **Sending more Alarm fields as annotations**: safe for identity, but Alertmanager cannot group or route on annotations, so it does not meet the need.
- **A fixed list of Alarm fields operators can turn on as labels**: copies values only, so it cannot derive a value such as a device class from a naming convention.
- **New Built-in labels**: every Alert would get a new identity on upgrade, causing a wave of duplicate firing and resolved notifications.
- **Node categories as a label**: the natural source for a device class, but they are not on the Alarm, and Horizon does not yet populate them on the inventory stream (#213).

## Consequences

- Adding, removing or changing a Label template gives every firing Alert it labels a new identity, as with an Extra label.
- The set of Alarm details is part of the receiver's interface: removing one would break templates that use it, so a detail is only added once it is known not to change while an Alarm is active.
- Templates do not apply to the `OpenNMSHeartbeat` Alert, which has no Alarm.
