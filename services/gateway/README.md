# Gateway runtime

MAX platform restrictions and cancellation notifications are disabled by default.

Apply the required notification, delivery-state and pending-state database migrations before enabling either option. The service checks writable storage and required privileges at startup when platform events are enabled.

| Environment variable | Default | Behavior |
|---|---|---|
| `GATEWAY_MAX_DELIVERY_EVENTS_ENABLED` | `false` | Persist platform stop/mute observations and attach them when a MAX account is created or reopened. |
| `GATEWAY_NOTIFICATION_DELIVERY_ENABLED` | `false` | Deliver queued cancellation notifications. Requires platform events, a configured bot username and the lifecycle receiver. |

Values must be valid booleans. Use the existing bot token and webhook secret configuration. The verified webhook subscription must include `bot_started`, `bot_stopped`, `dialog_removed`, `dialog_muted` and `dialog_unmuted` updates.

Notification consent is separate for each saved route and defaults to disabled. Platform events never grant consent. Turning notifications off suppresses queued attempts; an external request already in flight cannot be recalled. Resuming a bot or dialog does not revive suppressed messages.

The webhook and notification worker share one MAX client. Its per-recipient send gate is process-local; multiple instances require coordinated rate limiting. Delivery retries can produce duplicates after an ambiguous network result. Storage readiness and successful builds do not establish end-to-end delivery.

## Release order

Build the gateway, frontend and migration bundle from one compatible source revision. Use `compose.yaml` with `compose.product.yaml` to pass the feature flags into the gateway container. Setting a variable in `.env` alone does not pass it into a container without an environment mapping.

Apply storage migrations while both feature flags remain disabled, then configure the authenticated MAX webhook subscription. Enable platform event processing before notification delivery. Keep route consent disabled unless the owner explicitly enables it.

Disable notification delivery before a rollback. Keep the storage tables when rolling back application images so observations, queued results and consent history remain available. Downgrading database migrations removes that state and requires a separate data-retention decision.

`GATEWAY_SCENARIO_RESULT_DELIVERY_ENABLED` defaults to `false`. Enable it only after installing the scenario result queue and MAX delivery-state storage, configuring the bot, and enabling MAX delivery events. Startup checks the queue before accepting calculations. Each completed calculation records its result and delivery job in one transaction; technical failures and command replays do not enqueue another job. Route cancellation consent is independent.

The result worker uses the same bot client and send throttle as other bot messages. It retries temporary failures up to five attempts. A stopped or muted recipient suppresses pending jobs; resume does not revive them. An outdated scenario version is suppressed before sending. HTTP runs outside SQL transactions, so a stop or scenario change after the final check cannot retract an in-flight message. A crash after a successful send can also produce a duplicate. Disable result delivery before rolling back its application image; preserve the queue when rolling back.
