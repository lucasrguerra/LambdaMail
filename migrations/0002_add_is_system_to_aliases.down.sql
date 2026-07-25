-- Revert is_system column from aliases table
ALTER TABLE aliases DROP COLUMN IF EXISTS is_system;
