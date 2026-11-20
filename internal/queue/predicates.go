package queue

// Shared queue-membership predicates of ARS-005 §2.3. Each is a boolean SQL
// expression that refers only to the alias subject.video_id. Callers supply
// that alias and never edit the text.

// ErrorsQueueItem is true when the video has an error entry still in the
// Errors queue.
const ErrorsQueueItem = `EXISTS (SELECT 1 FROM error_entry e WHERE e.video_id = subject.video_id AND e.status IN ('active','retrying'))`

// ActiveQueueItem is true when the video has a live item in either queue.
const ActiveQueueItem = `(EXISTS (SELECT 1 FROM deferred_item d WHERE d.video_id = subject.video_id AND d.status IN ('pending','retrying'))
 OR ` + ErrorsQueueItem + `)`

// RemovedByUser is true when the most recent item over both queues was
// removed by the user (discarded or dismissed). Ties on time are broken by
// queue (errors after deferred) and then id.
const RemovedByUser = `(COALESCE((
  SELECT q.removed FROM (
    SELECT d.created_at AS at, 0 AS tbl, d.id AS id, (d.status = 'discarded') AS removed
      FROM deferred_item d WHERE d.video_id = subject.video_id
    UNION ALL
    SELECT e.created_at, 1, e.id, (e.status = 'dismissed')
      FROM error_entry e WHERE e.video_id = subject.video_id
  ) q ORDER BY julianday(q.at) DESC, q.tbl DESC, q.id DESC LIMIT 1), 0) = 1)`
