DROP TABLE IF EXISTS system_config;
ALTER TABLE mailboxes DROP COLUMN IF EXISTS auto_save_drafts;
ALTER TABLE mailboxes DROP COLUMN IF EXISTS signature;
