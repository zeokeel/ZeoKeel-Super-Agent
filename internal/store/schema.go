package store

// SchemaVersion is the version of the layout below. A database written by a
// newer version is refused rather than read wrongly: a half-understood store is
// worse than no store. The prototype learned this the hard way (SCHEMA_VERSION
// checks in src/app/store.ts).
//
// v3 folded the schedule/goal tables (and the H1 timeline/audit columns) into
// the ordered list. Before v3 those tables were created by store.EnsureV3, called
// by the packages that owned them, and marked in kv under "schema.schedule_goal"
// while the global stamp stayed at 2. That split had two version truths and could
// not be undone safely: v1/v2 use bare CREATE TABLE, so folding v3's tables into
// the ordered list would fail with "table already exists" on every file the M4
// builds had touched. The statements below are therefore idempotent
// (IF NOT EXISTS / addColumn), which is what lets one ordered migration serve a
// fresh file, a v2 file and an M4-era file that already has the tables.
const SchemaVersion = 3

// migration is one step of the ordered layout. Index n upgrades the schema from
// n to n+1.
type migration struct {
	name string
	// sql runs in order inside the migration transaction.
	sql []string
	// apply runs Go-side steps in the same transaction: additive columns (which
	// SQLite cannot express as CREATE IF NOT EXISTS), data normalization and
	// backfill. Optional.
	apply func(*Store) error
}

// migrations are applied in order; index n upgrades the schema from n to n+1.
var migrations = []migration{
	// 0 -> 1: the first layout. Every business table carries workspace_id, which
	// is an attribute and not a partition: one file holds many workspaces.
	{
		name: "base",
		sql: []string{
			`
CREATE TABLE workspace(
  id         TEXT PRIMARY KEY,
  name       TEXT NOT NULL,
  root       TEXT NOT NULL,
  remote     TEXT NOT NULL DEFAULT '',
  kind       TEXT NOT NULL,
  brief      TEXT NOT NULL,          -- JSON domain.Brief
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);

CREATE TABLE trigger(
  id                TEXT PRIMARY KEY,
  scope             TEXT NOT NULL,   -- 'global' or 'ws:<id>'
  workspace_id      TEXT,            -- NULL for a global Source's Trigger
  default_workspace TEXT,            -- what the Source is bound to, if any
  source            TEXT NOT NULL,
  at                TEXT NOT NULL,   -- fixed-width UTC, sorts lexicographically
  human             INTEGER NOT NULL,
  json              TEXT NOT NULL
);
CREATE INDEX trigger_by_scope_at ON trigger(scope, at DESC);
CREATE INDEX trigger_by_ws_at    ON trigger(workspace_id, at DESC);

CREATE TABLE disposition(
  trigger_id TEXT PRIMARY KEY REFERENCES trigger(id),
  kind       TEXT NOT NULL,
  reason     TEXT NOT NULL,          -- JSON []domain.Reason
  ticket_ids TEXT NOT NULL           -- JSON []domain.ID
);

CREATE TABLE thread(
  key          TEXT PRIMARY KEY,     -- '<scope>|<source>|<id>'
  workspace_id TEXT,
  source       TEXT NOT NULL,
  last_at      TEXT NOT NULL,
  json         TEXT NOT NULL
);
CREATE INDEX thread_by_ws_last ON thread(workspace_id, last_at DESC);

CREATE TABLE ticket(
  id           TEXT PRIMARY KEY,
  workspace_id TEXT NOT NULL,
  stage        TEXT NOT NULL,        -- cached StageOf output, never the truth
  ended_at     TEXT,                 -- NULL while open
  created_at   TEXT NOT NULL,
  json         TEXT NOT NULL
);
CREATE INDEX ticket_open ON ticket(workspace_id, created_at DESC) WHERE ended_at IS NULL;

CREATE TABLE decision(
  id           TEXT PRIMARY KEY,
  workspace_id TEXT,
  kind         TEXT NOT NULL,
  json         TEXT NOT NULL
);

CREATE TABLE effect(
  kind        TEXT NOT NULL,
  key         TEXT NOT NULL,
  seq         INTEGER NOT NULL,
  generation  INTEGER NOT NULL DEFAULT 0,
  attempts    INTEGER NOT NULL DEFAULT 0,
  due         TEXT NOT NULL,
  error       TEXT NOT NULL DEFAULT '',
  payload     TEXT NOT NULL DEFAULT '',
  PRIMARY KEY(kind, key)
);

CREATE TABLE outbox(
  key          TEXT PRIMARY KEY,
  workspace_id TEXT,
  ticket_id    TEXT,
  channel      TEXT NOT NULL,
  state        TEXT NOT NULL,        -- pending | sent | gone
  text         TEXT NOT NULL,
  created_at   TEXT NOT NULL
);
CREATE INDEX outbox_pending ON outbox(created_at) WHERE state = 'pending';

CREATE TABLE session_event(
  session_id TEXT NOT NULL,
  seq        INTEGER NOT NULL,
  at         TEXT NOT NULL,
  kind       TEXT NOT NULL,
  payload    TEXT NOT NULL,
  PRIMARY KEY(session_id, seq)
);

CREATE TABLE health(
  entry   TEXT PRIMARY KEY,
  state   TEXT NOT NULL,
  detail  TEXT NOT NULL DEFAULT '',
  at      TEXT NOT NULL
);

CREATE TABLE kv(
  k TEXT PRIMARY KEY,
  v TEXT NOT NULL
);
`,
		},
	},

	// 1 -> 2: the columns the durable half needs. Nothing in v1 is renamed or
	// dropped, so a v1 file upgrades in place. Three of these are what make the
	// mechanisms in design §7.2 work:
	//
	//   workspace.policy  the engine's Policy is what gates a Source; without it
	//                     a restart would rebuild the workspaces but forget who
	//                     may ask for work there.
	//   effect.since      when the row was first queued; the effect queue reports
	//                     "waiting since" for its health entry.
	//   outbox.conversation/thread/reply_to
	//                     per-Ticket per-conversation ordering and the reply
	//                     target, kept on the receipt rather than the effect
	//                     payload so a settled receipt is still a complete audit
	//                     record.
	//
	// Time stored by the durable half is fixed-width UTC so text comparison is
	// chronological (`due <= ?` runs in SQL). See FormatTime.
	{
		name: "durable-columns",
		sql: []string{
			`ALTER TABLE workspace ADD COLUMN policy TEXT NOT NULL DEFAULT '{}'`,

			`ALTER TABLE effect ADD COLUMN since TEXT NOT NULL DEFAULT ''`,
			`CREATE INDEX effect_by_seq ON effect(kind, seq)`,
			`CREATE INDEX effect_by_due ON effect(due)`,

			`ALTER TABLE outbox ADD COLUMN conversation TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE outbox ADD COLUMN thread       TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE outbox ADD COLUMN reply_to     TEXT NOT NULL DEFAULT ''`,
			`ALTER TABLE outbox ADD COLUMN kind         TEXT NOT NULL DEFAULT 'reply'`,
			`ALTER TABLE outbox ADD COLUMN updated_at   TEXT NOT NULL DEFAULT ''`,
			`CREATE INDEX outbox_order ON outbox(ticket_id, channel, conversation, created_at)
			  WHERE state = 'pending'`,

			`CREATE INDEX ticket_by_created ON ticket(created_at DESC)`,
			`CREATE INDEX decision_by_kind  ON decision(kind)`,
		},
	},

	// 2 -> 3: the M4 capabilities (schedules and goals) and the H1 read paths the
	// prototype relies on. Additive only; a v2 file upgrades in place.
	//
	//   schedule / schedule_fire  durable timer bookkeeping. next_at is fixed-width
	//                             UTC so "due" is one indexed comparison.
	//   goal                      immutable objective + mutable control state.
	//   trigger_thread            the Trigger↔Thread inverted index: a page of a
	//                             Thread's timeline continues from an <at>|<id>
	//                             cursor and Triggers that share a time are not
	//                             skipped. PutTrigger keeps it up to date; the
	//                             apply step backfills rows written before it.
	//   decision.thread           lets the panel list Decisions by Thread instead
	//                             of reversing an in-memory projection.
	//   outbox.dropped_at/reason  a `gone` receipt says when and why it can never
	//                             be delivered, instead of only carrying state.
	{
		name: "schedule-goal-timeline",
		sql: []string{
			`CREATE TABLE IF NOT EXISTS schedule(
  id               TEXT PRIMARY KEY,
  workspace_id     TEXT,
  spec             TEXT NOT NULL,        -- JSON schedule.Spec
  enabled          INTEGER NOT NULL DEFAULT 1,
  created_at       TEXT NOT NULL,
  updated_at       TEXT NOT NULL,
  last_fired_at    TEXT,
  next_at          TEXT,
  running_fire     TEXT NOT NULL DEFAULT '',
  last_status      TEXT NOT NULL DEFAULT '',
  last_skip_reason TEXT NOT NULL DEFAULT '',
  last_skip_at     TEXT
)`,
			`CREATE INDEX IF NOT EXISTS schedule_due ON schedule(enabled, next_at)`,

			`CREATE TABLE IF NOT EXISTS schedule_fire(
  id          TEXT PRIMARY KEY,
  schedule_id TEXT NOT NULL,
  planned_at  TEXT NOT NULL,
  fired_at    TEXT NOT NULL,
  status      TEXT NOT NULL,            -- fired | skipped | completed | interrupted
  reason      TEXT NOT NULL DEFAULT '',
  created_at  TEXT NOT NULL
)`,
			`CREATE INDEX IF NOT EXISTS schedule_fire_by_schedule ON schedule_fire(schedule_id, fired_at DESC)`,

			`CREATE TABLE IF NOT EXISTS goal(
  id                  TEXT PRIMARY KEY,
  workspace_id        TEXT NOT NULL,
  state               TEXT NOT NULL,
  revision            INTEGER NOT NULL,
  objective           TEXT NOT NULL,
  max_rounds          INTEGER NOT NULL,
  block_threshold     INTEGER NOT NULL,
  blocked_reason      TEXT NOT NULL DEFAULT '',
  consecutive_blocked INTEGER NOT NULL DEFAULT 0,
  rounds_count        INTEGER NOT NULL DEFAULT 0,
  created_at          TEXT NOT NULL,
  updated_at          TEXT NOT NULL,
  json                TEXT NOT NULL
)`,
			`CREATE INDEX IF NOT EXISTS goal_by_ws ON goal(workspace_id, updated_at DESC)`,
			`CREATE INDEX IF NOT EXISTS goal_live ON goal(state, updated_at) WHERE state != 'completed'`,

			`CREATE TABLE IF NOT EXISTS trigger_thread(
  thread TEXT NOT NULL,               -- domain.ThreadKey: '<scope>|<source>|<id>'
  id     TEXT NOT NULL,               -- trigger id
  at     TEXT NOT NULL,               -- fixed-width UTC, the timeline cursor
  human  INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY(thread, id)
)`,
			`CREATE INDEX IF NOT EXISTS trigger_thread_at ON trigger_thread(thread, at DESC, id DESC)`,
		},
		apply: applyV3,
	},
}
