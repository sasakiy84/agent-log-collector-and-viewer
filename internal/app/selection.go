package app

// Keep membership and its parameters together. Search analysis can change the
// driving table without parsing generated SQL or duplicating filter predicates.
type sqlSelection struct {
	from, where string
	args        []any
}

func (s sqlSelection) sql() string { return s.from + " WHERE 1" + s.where }
