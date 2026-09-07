# Alert lifecycle

The hub evaluates enabled rules every 30 seconds. Active and acknowledged
alerts are both unresolved incidents: acknowledgment hides an alert from the
active count but does not make the same ongoing condition notify again.
Recovery resolves both states. A later breach starts a new incident.

`duration_secs` waits for a continuously observed breach before the first
notification. A healthy reading, unavailable/stale sensor, disabled/changed
rule, evaluation gap over 90 seconds, or hub restart resets that pending wait.
This small in-memory timer is not historical sampling; evaluation cadence
limits its precision. It does not delay recovery of an existing incident.

CPU/RAM/disk/GPU thresholds require fresh metrics and an online machine.
Missing data is unknown, not zero and not evidence of recovery. Offline
duration rules use the last-seen timestamp separately. Legacy GPU temperature
zero still means unavailable because older agents use it as a missing value.

Database and dashboard events are updated before Telegram delivery. Telegram
has a five-second per-request timeout and a ten-second total evaluation-batch
budget. Failed or undelivered notifications remain visible in the dashboard;
there is no unbounded retry queue. No real Telegram credentials are needed for
the regression tests, which use a local fake server.

Cleanup removes resolved history older than 30 days, never an acknowledged
incident that is still ongoing.
