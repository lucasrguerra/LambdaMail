-- Irreversible by nature: this migration replaced incorrect counter values and
-- added the flags that describe what a message is. Restoring the old numbers
-- would mean restoring known-wrong data, and dropping the \Seen and \Draft
-- flags would discard read state the user may since have set by hand.
SELECT 1;
