package store

import "context"

// RecordLinks are, for the records of a host that point at another
// record, the fingerprint of that record. A fingerprint names a record
// across ledgers, where an ID does not.
type RecordLinks struct {
	// EventDup: an event that reports the same error as another event.
	EventDup map[int64]string
	// ActionDup: an action that reports the same step as another action.
	ActionDup map[int64]string
	// Withdrawn: a mark that a later event took back.
	Withdrawn map[int64]string
}

// Links returns the links of the records of a host.
func (s *Store) Links(ctx context.Context, host string) (RecordLinks, error) {
	l := RecordLinks{EventDup: map[int64]string{}, ActionDup: map[int64]string{}, Withdrawn: map[int64]string{}}
	for _, q := range []struct {
		sql string
		to  map[int64]string
	}{
		{`SELECT d.id, o.fingerprint FROM event d JOIN event o ON o.id = d.dup_of WHERE d.host = ? AND d.dup_of IS NOT NULL`, l.EventDup},
		{`SELECT d.id, o.fingerprint FROM action d JOIN action o ON o.id = d.dup_of WHERE d.host = ? AND d.dup_of IS NOT NULL`, l.ActionDup},
		{`SELECT a.id, e.fingerprint FROM action a JOIN event e ON e.id = a.withdrawn_by WHERE a.host = ? AND a.withdrawn_by IS NOT NULL`, l.Withdrawn},
	} {
		rows, err := s.db.QueryContext(ctx, q.sql, host)
		if err != nil {
			return l, err
		}
		for rows.Next() {
			var id int64
			var fp string
			if err := rows.Scan(&id, &fp); err != nil {
				rows.Close()
				return l, err
			}
			q.to[id] = fp
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return l, err
		}
	}
	return l, nil
}
