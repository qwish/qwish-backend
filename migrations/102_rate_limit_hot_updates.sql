-- Every rate-limited request updates its row's tat. An index on tat made each
-- of those a non-HOT update: a new index entry and a dead tuple per request
-- (load test: 207k updates, 0 HOT). The index only served the once-a-minute
-- expiry DELETE, which can scan this small table instead. Spare page space lets
-- the update stay on the same page.
DROP INDEX IF EXISTS distributed_rate_limits_expiry;
ALTER TABLE distributed_rate_limits SET (fillfactor = 70);
