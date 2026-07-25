-- Add is_system column to aliases table to track mandatory system security aliases
ALTER TABLE aliases ADD COLUMN is_system BOOLEAN NOT NULL DEFAULT false;
