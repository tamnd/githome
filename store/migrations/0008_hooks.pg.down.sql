-- 0008_hooks (postgres, down): drop the activity and webhook surface, children
-- first so the foreign keys never block.
DROP TABLE webhook_deliveries;
DROP TABLE webhooks;
DROP TABLE events;
