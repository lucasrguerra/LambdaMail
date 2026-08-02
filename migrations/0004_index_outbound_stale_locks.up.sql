-- Delivery workers claim a job by moving it to SENDING with a lock deadline.
-- If the worker dies before releasing it, the job must be picked up again once
-- the lock expires; without this index that reclaim scan is a sequential scan
-- over every job ever queued.
CREATE INDEX idx_outbound_stale_locks ON outbound_jobs(locked_until)
    WHERE status = 'SENDING';
