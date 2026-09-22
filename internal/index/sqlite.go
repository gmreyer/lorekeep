package index

import (
	"database/sql"
	"fmt"
	"os"

	_ "modernc.org/sqlite"
)

// ddl is the index schema.
//
// Edges are stored forward only, with an index on target. An inverse is
// traversed by running the forward relation backwards, so materialising
// inverse rows would double the table and create a second place for the same
// fact — the thing "declared once, derived, never authored twice" exists to
// prevent.
//
// Conditions are denormalised into one table keyed by what owns them, because
// "what is conditional on this decision" is the question a writer asks, and
// answering it should not mean unioning five tables.
const ddl = `
CREATE TABLE meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE entity_types (
  name        TEXT PRIMARY KEY,
  description TEXT NOT NULL DEFAULT ''
);

CREATE TABLE groups (
  name TEXT NOT NULL,
  type TEXT NOT NULL,
  PRIMARY KEY (name, type)
);

CREATE TABLE roles (
  name        TEXT PRIMARY KEY,
  description TEXT NOT NULL DEFAULT ''
);

CREATE TABLE relations (
  name        TEXT PRIMARY KEY,
  inverse     TEXT NOT NULL DEFAULT '',
  symmetric   INTEGER NOT NULL DEFAULT 0,
  role        TEXT NOT NULL DEFAULT '',
  description TEXT NOT NULL DEFAULT ''
);

-- The two tables mirror what the pack declares, which may be a group: a
-- relation reading "agent -> event" says something an expansion would lose,
-- and an editor form needs it to offer the right picker. The two views answer
-- the question a query actually has — may a character stand here — by
-- expanding through groups, so nothing has to choose between the two.
CREATE TABLE relation_domain (relation TEXT NOT NULL, type TEXT NOT NULL, PRIMARY KEY (relation, type));
CREATE TABLE relation_range  (relation TEXT NOT NULL, type TEXT NOT NULL, PRIMARY KEY (relation, type));

CREATE TABLE eras (key TEXT PRIMARY KEY, name TEXT NOT NULL DEFAULT '', ordinal INTEGER NOT NULL);
CREATE TABLE acts (key TEXT PRIMARY KEY, name TEXT NOT NULL DEFAULT '', ordinal INTEGER NOT NULL);

-- era/earliest/latest/precision hold whichever interval the entity carries: a
-- character's lifespan or an event's date. An entity has at most one, so one
-- set of columns serves both and the type says which it is.
CREATE TABLE entities (
  id              TEXT PRIMARY KEY,
  type            TEXT NOT NULL,
  name            TEXT NOT NULL,
  status          TEXT NOT NULL,
  visibility      TEXT NOT NULL,
  visibility_kind TEXT NOT NULL,
  visibility_act  TEXT,
  era             TEXT,
  earliest        INTEGER,
  latest          INTEGER,
  precision       TEXT,
  file            TEXT NOT NULL,
  body            TEXT NOT NULL DEFAULT ''
);

CREATE TABLE aliases (
  entity TEXT NOT NULL,
  alias  TEXT NOT NULL,
  ord    INTEGER NOT NULL,
  PRIMARY KEY (entity, ord)
);

CREATE TABLE edges (
  id       INTEGER PRIMARY KEY,
  source   TEXT NOT NULL,
  relation TEXT NOT NULL,
  target   TEXT NOT NULL,
  priority INTEGER,
  ord      INTEGER NOT NULL,
  note     TEXT NOT NULL DEFAULT ''
);
CREATE INDEX edges_source ON edges (source, relation);
CREATE INDEX edges_target ON edges (target, relation);

CREATE TABLE statements (
  id              TEXT PRIMARY KEY,
  name            TEXT NOT NULL DEFAULT '',
  subject         TEXT NOT NULL,
  relation        TEXT NOT NULL,
  object          TEXT NOT NULL,
  truth           TEXT NOT NULL,
  status          TEXT NOT NULL,
  visibility      TEXT NOT NULL,
  visibility_kind TEXT NOT NULL,
  visibility_act  TEXT,
  file            TEXT NOT NULL,
  body            TEXT NOT NULL DEFAULT ''
);
CREATE INDEX statements_subject ON statements (subject, relation);
CREATE INDEX statements_object  ON statements (object, relation);

CREATE TABLE beliefs (
  id             INTEGER PRIMARY KEY,
  agent          TEXT NOT NULL,
  statement      TEXT NOT NULL,
  value          INTEGER NOT NULL,
  confidence     TEXT NOT NULL DEFAULT '',
  since_era      TEXT,
  since_earliest INTEGER,
  since_latest   INTEGER,
  acquired_from  TEXT,
  ord            INTEGER NOT NULL
);
CREATE INDEX beliefs_agent     ON beliefs (agent);
CREATE INDEX beliefs_statement ON beliefs (statement);

CREATE TABLE assertions (
  id            INTEGER PRIMARY KEY,
  agent         TEXT NOT NULL,
  statement     TEXT NOT NULL,
  value         INTEGER NOT NULL,
  audience      TEXT,
  when_era      TEXT,
  when_earliest INTEGER,
  when_latest   INTEGER,
  ord           INTEGER NOT NULL
);
CREATE INDEX assertions_agent     ON assertions (agent);
CREATE INDEX assertions_statement ON assertions (statement);

CREATE TABLE outcomes (
  decision TEXT NOT NULL,
  outcome  TEXT NOT NULL,
  ord      INTEGER NOT NULL,
  PRIMARY KEY (decision, outcome)
);

CREATE TABLE conditions (
  owner_kind TEXT NOT NULL,
  owner      TEXT NOT NULL,
  decision   TEXT NOT NULL,
  outcome    TEXT NOT NULL,
  ord        INTEGER NOT NULL
);
CREATE INDEX conditions_owner    ON conditions (owner_kind, owner);
CREATE INDEX conditions_decision ON conditions (decision, outcome);

CREATE VIEW relation_domain_types AS
  SELECT relation, type FROM relation_domain WHERE type IN (SELECT name FROM entity_types)
  UNION
  SELECT rd.relation, g.type FROM relation_domain rd JOIN groups g ON g.name = rd.type;

CREATE VIEW relation_range_types AS
  SELECT relation, type FROM relation_range WHERE type IN (SELECT name FROM entity_types)
  UNION
  SELECT rr.relation, g.type FROM relation_range rr JOIN groups g ON g.name = rr.type;

-- Exact matching over names, aliases and prose. Not vector search: at a few
-- hundred entities, exact matching outperforms semantic similarity and is far
-- easier to debug.
CREATE VIRTUAL TABLE search USING fts5 (
  id UNINDEXED, kind UNINDEXED, name, aliases, body
);
`

// Owner kinds for the conditions table.
const (
	ownerEntity    = "entity"
	ownerStatement = "statement"
	ownerEdge      = "edge"
	ownerBelief    = "belief"
	ownerAssertion = "assertion"
)

// WriteSQLite builds the index database at path, replacing anything there.
//
// The index is disposable: it is rebuilt from the repository and never edited
// in place, so removing the old file outright is correct rather than careless.
func WriteSQLite(idx *Index, path string) (err error) {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("clearing the old index: %w", err)
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return fmt.Errorf("opening the index: %w", err)
	}
	defer func() {
		if cerr := db.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	if _, err := db.Exec(ddl); err != nil {
		return fmt.Errorf("creating the index schema: %w", err)
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			_ = tx.Rollback()
		}
	}()

	w := &sqlWriter{tx: tx}
	w.vocabulary(idx)
	w.entities(idx)
	w.statements(idx)
	if w.err != nil {
		return w.err
	}
	return tx.Commit()
}

// sqlWriter carries the first error rather than returning one from every
// insert, so the write reads as the sequence of tables it is.
type sqlWriter struct {
	tx  *sql.Tx
	err error
}

func (w *sqlWriter) exec(query string, args ...any) {
	if w.err != nil {
		return
	}
	if _, err := w.tx.Exec(query, args...); err != nil {
		w.err = fmt.Errorf("%s: %w", query, err)
	}
}

func (w *sqlWriter) vocabulary(idx *Index) {
	for _, kv := range [][2]string{
		{"format", fmt.Sprint(idx.Format)},
		{"pack_name", idx.Pack.Name},
		{"pack_version", idx.Pack.Version},
		{"core_version", idx.Pack.CoreVersion},
	} {
		w.exec(`INSERT INTO meta (key, value) VALUES (?, ?)`, kv[0], kv[1])
	}

	for _, t := range idx.Vocabulary.EntityTypes {
		w.exec(`INSERT INTO entity_types (name, description) VALUES (?, ?)`, t.Name, t.Description)
	}
	for _, g := range idx.Vocabulary.Groups {
		for _, t := range g.Types {
			w.exec(`INSERT INTO groups (name, type) VALUES (?, ?)`, g.Name, t)
		}
	}
	for _, r := range idx.Vocabulary.Roles {
		w.exec(`INSERT INTO roles (name, description) VALUES (?, ?)`, r.Name, r.Description)
	}
	for _, r := range idx.Vocabulary.Relations {
		w.exec(`INSERT INTO relations (name, inverse, symmetric, role, description) VALUES (?, ?, ?, ?, ?)`,
			r.Name, r.Inverse, boolToInt(r.Symmetric), r.Role, r.Description)
		for _, t := range r.Domain {
			w.exec(`INSERT INTO relation_domain (relation, type) VALUES (?, ?)`, r.Name, t)
		}
		for _, t := range r.Range {
			w.exec(`INSERT INTO relation_range (relation, type) VALUES (?, ?)`, r.Name, t)
		}
	}
	for _, e := range idx.Vocabulary.Eras {
		w.exec(`INSERT INTO eras (key, name, ordinal) VALUES (?, ?, ?)`, e.Key, e.Name, e.Ordinal)
	}
	for _, a := range idx.Vocabulary.Acts {
		w.exec(`INSERT INTO acts (key, name, ordinal) VALUES (?, ?, ?)`, a.Key, a.Name, a.Ordinal)
	}
}

func (w *sqlWriter) entities(idx *Index) {
	for _, e := range idx.Entities {
		kind, act := splitVisibility(e.Visibility)
		era, earliest, latest, precision := splitInterval(e.Interval)

		w.exec(`INSERT INTO entities
			(id, type, name, status, visibility, visibility_kind, visibility_act,
			 era, earliest, latest, precision, file, body)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			e.ID, e.Type, e.Name, e.Status, e.Visibility, kind, nullableString(act),
			era, earliest, latest, precision, e.File, e.Body)

		for i, alias := range e.Aliases {
			w.exec(`INSERT INTO aliases (entity, alias, ord) VALUES (?, ?, ?)`, e.ID, alias, i)
		}
		for i, out := range e.Outcomes {
			w.exec(`INSERT INTO outcomes (decision, outcome, ord) VALUES (?, ?, ?)`, e.ID, out, i)
		}
		w.conditions(ownerEntity, e.ID, e.ValidIn)

		for i, edge := range e.Edges {
			w.exec(`INSERT INTO edges (id, source, relation, target, priority, ord, note)
				VALUES (?, ?, ?, ?, ?, ?, ?)`,
				edge.ID, e.ID, edge.Relation, edge.Target, nullableInt(edge.Priority), i, edge.Note)
			w.conditions(ownerEdge, fmt.Sprint(edge.ID), edge.ValidIn)
		}
		for i, b := range e.Beliefs {
			era, earliest, latest, _ := splitInterval(b.Since)
			w.exec(`INSERT INTO beliefs
				(id, agent, statement, value, confidence, since_era, since_earliest, since_latest, acquired_from, ord)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				b.ID, e.ID, b.Statement, boolToInt(b.Value), b.Confidence,
				era, earliest, latest, nullableString(b.AcquiredFrom), i)
			w.conditions(ownerBelief, fmt.Sprint(b.ID), b.ValidIn)
		}
		for i, a := range e.Assertions {
			era, earliest, latest, _ := splitInterval(a.When)
			w.exec(`INSERT INTO assertions
				(id, agent, statement, value, audience, when_era, when_earliest, when_latest, ord)
				VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				a.ID, e.ID, a.Statement, boolToInt(a.Value), nullableString(a.Audience), era, earliest, latest, i)
			w.conditions(ownerAssertion, fmt.Sprint(a.ID), a.ValidIn)
		}

		w.exec(`INSERT INTO search (id, kind, name, aliases, body) VALUES (?, ?, ?, ?, ?)`,
			e.ID, ownerEntity, e.Name, joinAliases(e.Aliases), e.Body)
	}
}

func (w *sqlWriter) statements(idx *Index) {
	for _, s := range idx.Statements {
		kind, act := splitVisibility(s.Visibility)
		w.exec(`INSERT INTO statements
			(id, name, subject, relation, object, truth, status, visibility, visibility_kind, visibility_act, file, body)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			s.ID, s.Name, s.Subject, s.Relation, s.Object, s.Truth, s.Status,
			s.Visibility, kind, nullableString(act), s.File, s.Body)
		w.conditions(ownerStatement, s.ID, s.ValidIn)

		w.exec(`INSERT INTO search (id, kind, name, aliases, body) VALUES (?, ?, ?, ?, ?)`,
			s.ID, ownerStatement, s.Name, "", s.Body)
	}
}

func (w *sqlWriter) conditions(kind, owner string, conds []Condition) {
	for i, c := range conds {
		w.exec(`INSERT INTO conditions (owner_kind, owner, decision, outcome, ord) VALUES (?, ?, ?, ?, ?)`,
			kind, owner, c.Decision, c.Outcome, i)
	}
}
