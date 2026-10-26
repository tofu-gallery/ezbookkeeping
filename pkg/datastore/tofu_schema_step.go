package datastore

// Tofu (2026-10-26): this file is Tofu's addition; it is not part of upstream ezBookkeeping.

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/lib/pq"
	"xorm.io/xorm"

	"github.com/mayswind/ezbookkeeping/pkg/core"
	"github.com/mayswind/ezbookkeeping/pkg/log"
	"github.com/mayswind/ezbookkeeping/pkg/settings"
)

// The start-up schema step on PostgreSQL.
//
// Tofu starts several copies of a new deployment within a second or two, all against the same database, and every
// copy syncs all tables before it listens. On an empty database they created the same tables at the same moment and
// crashed against each other ("duplicate key value violates unique constraint pg_type_typname_nsp_index", "relation
// ... already exists"), and a crashed start answers HTTP 500. Here each start does all of its schema work inside ONE
// transaction whose first statement takes a transaction-level advisory lock: one start does the work, the others wait
// for it, then each reads the state again inside the lock and finds the work done.
//
// Tofu's DATABASE_URL is a transaction pooler. The pooler keeps one server connection for a transaction from its first
// statement to its COMMIT or ROLLBACK, and pg_advisory_xact_lock is released by exactly that COMMIT or ROLLBACK, so the
// lock covers every statement of the transaction on that server connection and nothing after it. A session lock
// (pg_advisory_lock) would stay behind on a server connection that the pooler hands to other clients.
const (
	// schemaStepLockKey is the advisory lock key every start of this app takes: the bytes of "ezbk"
	schemaStepLockKey = 0x657a626b

	// schemaStepLockTimeout bounds how long a start waits for another start's schema step; then it fails
	schemaStepLockTimeout = "120s"

	// schemaStateTable records the build that last completed the step on this database
	schemaStateTable = "tofu_schema_state"
	schemaStateName  = "ezbookkeeping"

	// schemaStepVersion changes when this step itself changes, so that every database goes through the new step once
	schemaStepVersion = "tofu-schema-step-1"
)

// UpdateStructure runs sync, which syncs every table of the app, as the start-up schema step. On a single PostgreSQL
// database it runs inside one transaction holding the schema step's advisory lock, followed in the same transaction by
// row-level security on every table it synced; unless force is set, it is skipped when this same build already
// completed it on this database. A start that meets an object another start created at the same moment runs the whole
// step once more. On any other database sync runs as upstream runs it.
func (s *DataStoreContainer) UpdateStructure(c core.Context, force bool, sync func() error) error {
	db := s.singlePostgresDatabase()

	if db == nil {
		return sync()
	}

	fingerprint := schemaStepFingerprint()
	err := db.runSchemaStep(c, force, fingerprint, sync)

	if err != nil && isDuplicateObjectError(err) {
		log.BootWarnf(c, "[datastore.UpdateStructure] the schema step met an object another start created at the same time (%s), so it runs once more", err.Error())
		err = db.runSchemaStep(c, force, fingerprint, sync)
	}

	return err
}

func (s *DataStoreContainer) singlePostgresDatabase() *Database {
	var db *Database

	for _, store := range []*DataStore{s.UserStore, s.TokenStore, s.UserDataStore} {
		if store == nil || store.Count() != 1 {
			return nil
		}

		if db == nil {
			db = store.Get(0)
		} else if store.Get(0) != db {
			return nil
		}
	}

	if db == nil || db.databaseType != settings.PostgresDbType {
		return nil
	}

	return db
}

func (db *Database) runSchemaStep(c core.Context, force bool, fingerprint string, sync func() error) error {
	return db.DoTransaction(nil, func(sess *xorm.Session) error {
		locked, err := sess.QueryString(fmt.Sprintf("SELECT CASE WHEN pg_try_advisory_xact_lock(%d) THEN 1 ELSE 0 END AS locked", schemaStepLockKey))

		if err != nil {
			return err
		}

		if len(locked) != 1 || locked[0]["locked"] != "1" {
			log.BootInfof(c, "[datastore.runSchemaStep] another start is updating the database tables, waiting for it (at most %s)", schemaStepLockTimeout)

			if _, err = sess.Exec(fmt.Sprintf("SET LOCAL lock_timeout = '%s'; SELECT pg_advisory_xact_lock(%d)", schemaStepLockTimeout, schemaStepLockKey)); err != nil {
				return err
			}
		}

		// Inside the lock: what the last completed step left, never an assumption that the database is still empty
		if _, err = sess.Exec("CREATE TABLE IF NOT EXISTS " + schemaStateTable + " (name VARCHAR(64) PRIMARY KEY, fingerprint VARCHAR(128) NOT NULL, updated_unix_time BIGINT NOT NULL)"); err != nil {
			return err
		}

		state, err := sess.QueryString("SELECT fingerprint FROM " + schemaStateTable + " WHERE name = '" + schemaStateName + "'")

		if err != nil {
			return err
		}

		if !force && fingerprint != "" && len(state) == 1 && state[0]["fingerprint"] == fingerprint {
			log.BootInfof(c, "[datastore.runSchemaStep] database tables are already up to date for this build")
			return nil
		}

		db.schemaSession = sess
		db.schemaTables = nil
		err = sync()
		tables := append(db.schemaTables, schemaStateTable)
		db.schemaSession = nil
		db.schemaTables = nil

		if err != nil {
			return err
		}

		if _, err = sess.Exec(rowLevelSecuritySQL(tables)); err != nil {
			return err
		}

		if fingerprint != "" {
			_, err = sess.Exec(fmt.Sprintf("INSERT INTO %s (name, fingerprint, updated_unix_time) VALUES ('%s', '%s', %d) ON CONFLICT (name) DO UPDATE SET fingerprint = EXCLUDED.fingerprint, updated_unix_time = EXCLUDED.updated_unix_time",
				schemaStateTable, schemaStateName, fingerprint, time.Now().Unix()))

			if err != nil {
				return err
			}
		}

		log.BootInfof(c, "[datastore.runSchemaStep] database tables updated, with row-level security on %d tables", len(tables))
		return nil
	})
}

// syncStructsInSchemaStep syncs beans in the schema step's transaction and remembers their tables
func (db *Database) syncStructsInSchemaStep(beans ...any) error {
	for _, bean := range beans {
		db.schemaTables = append(db.schemaTables, db.engineGroup.TableName(bean))
	}

	return db.schemaSession.Sync(beans...)
}

// schemaStepFingerprint names this build: the SHA-256 of the running executable, after the build's version, commit,
// build time and this step's version. The tables the step syncs are this build's own models, so a database that this
// build already took through the step needs nothing more, and any other build, older or newer, runs the full step once.
// It is empty when the executable cannot be read, and then the full step runs at every start.
func schemaStepFingerprint() string {
	path, err := os.Executable()

	if err != nil {
		return ""
	}

	file, err := os.Open(path)

	if err != nil {
		return ""
	}

	defer file.Close()

	hash := sha256.New()
	_, _ = io.WriteString(hash, strings.Join([]string{schemaStepVersion, core.Version, core.CommitHash, core.BuildTime}, "\n")+"\n")

	if _, err = io.Copy(hash, file); err != nil {
		return ""
	}

	return hex.EncodeToString(hash.Sum(nil))
}

// rowLevelSecuritySQL turns on row-level security for the given tables of the current schema, in one statement, on a
// database that has the roles of Supabase's Data API (anon, authenticated). Today's Tofu databases still grant every
// new table in public to those roles, so without it anyone holding the project's public key could read and change
// ezBookkeeping's tables through the Data API. With row-level security on and no policy, those roles see no row and
// may add none, while ezBookkeeping's own connection, which owns the tables, is not limited by it. A table that already
// has it, or that ezBookkeeping's role does not own, is left as it is.
func rowLevelSecuritySQL(tables []string) string {
	names := make([]string, 0, len(tables))

	for _, table := range tables {
		names = append(names, "'"+strings.ReplaceAll(table, "'", "''")+"'")
	}

	return `DO $tofu$
DECLARE
	t record;
BEGIN
	IF NOT EXISTS (SELECT 1 FROM pg_catalog.pg_roles WHERE rolname IN ('anon', 'authenticated')) THEN
		RETURN;
	END IF;

	FOR t IN
		SELECT n.nspname, c.relname
		FROM pg_catalog.pg_class c
		JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = pg_catalog.current_schema()
			AND c.relkind IN ('r', 'p')
			AND NOT c.relrowsecurity
			AND c.relowner = (SELECT oid FROM pg_catalog.pg_roles WHERE rolname = current_user)
			AND c.relname = ANY (ARRAY[` + strings.Join(names, ", ") + `]::text[])
	LOOP
		EXECUTE pg_catalog.format('ALTER TABLE %I.%I ENABLE ROW LEVEL SECURITY', t.nspname, t.relname);
	END LOOP;
END
$tofu$`
}

// isDuplicateObjectError tells whether err is PostgreSQL refusing to create an object that already exists
func isDuplicateObjectError(err error) bool {
	var pqErr *pq.Error

	if !errors.As(err, &pqErr) {
		return false
	}

	switch string(pqErr.Code) {
	case "42P07", // duplicate_table: a table or an index
		"42710", // duplicate_object
		"42701", // duplicate_column
		"23505": // unique_violation: two tables of one name collide in pg_type
		return true
	}

	return false
}
