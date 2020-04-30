/*
 * Copyright 2020 Torben Schinke
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package sqlm

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

const createMigrationTable = `CREATE TABLE IF NOT EXISTS "migration_schema_history"
(
    "group"              VARCHAR(255) NOT NULL,
    "version"            BIGINT       NOT NULL,
    "script"             VARCHAR(255) NOT NULL,
    "type"               VARCHAR(12)  NOT NULL,
    "checksum"           CHAR(64)     NOT NULL,
    "applied_at"         TIMESTAMP    NOT NULL,
    "execution_duration" BIGINT       NOT NULL,
    "status"             VARCHAR(12)  NOT NULL,
    "log"                TEXT         NOT NULL,
    PRIMARY KEY ("group", "version")
)`

type Type string
type Status string
type DBType string

// actually a bad code smell but this is intentional, bcause we explicitly don't want concurrent migrations
// within a single process (actually not even between processes), for your own brains sake.
var mutex sync.Mutex

const (
	SQL        Type   = "sql"
	Success    Status = "success"
	Failed     Status = "failed"
	Pending    Status = "pending"
	Executing  Status = "executing"
	PostgreSQL DBType = "postgresql"
	MySQL      DBType = "mysql"
)

type HistoryEntry struct {
	Group             string
	Version           int64
	Script            string
	Type              Type
	Checksum          string
	AppliedAt         time.Time
	ExecutionDuration time.Duration
	Status            Status
	Log               string
}

type Migration = struct {
	Group      string
	Version    int64
	Statements []string
	ScriptName string
}

func hash(m Migration) string {
	sum := sha256.Sum256([]byte(strings.Join(m.Statements, ";")))
	return hex.EncodeToString(sum[:])
}

type DB interface {
	Exec(query string, args ...interface{}) (sql.Result, error)
	Query(query string, args ...interface{}) (*sql.Rows, error)
}

// MustMigrate panics, if the migrations cannot be applied.
// Creates a transaction and tries a rollback, before bailing out.
func MustMigrate(db *sql.DB, migrations ...Migration) {
	tx, err := db.BeginTx(context.Background(), nil)
	if err != nil {
		panic(err)
	}
	if err := Apply(tx, migrations...); err != nil {
		if suppressedErr := tx.Rollback(); suppressedErr != nil {
			fmt.Println(suppressedErr.Error())
		}
		panic(err)
	}
	if err := tx.Commit(); err != nil {
		panic(err)
	}
}

func Apply(db DB, migrations ...Migration) error {
	mutex.Lock()
	defer mutex.Unlock()

	dbType, err := version(db)
	if err != nil {
		return fmt.Errorf("unknown database type: %w", err)
	}

	if err := CreateTable(db); err != nil {
		return fmt.Errorf("cannot create migration table: %w", err)
	}

	entries, err := History(db)
	if err != nil {
		return fmt.Errorf("cannot get history: %w", err)
	}

	for _, entry := range entries {
		if entry.Status != Success {
			return fmt.Errorf("migrations are dirty. Needs manual fix: %+v", entry)
		}
	}

	// group by and pick those things, which have not been applied yet
	groups := make(map[string][]Migration)
	for _, migration := range migrations {
		alreadyApplied := false
		for _, entry := range entries {
			if migration.Group == entry.Group {
				if migration.Version == entry.Version {
					if hash(migration) != entry.Checksum {
						return fmt.Errorf("an already applied migration has been modified. Needs manual fix: %v vs %v", entry, migration)
					}
					alreadyApplied = true
					//fmt.Printf("migration already applied: %s.%d\n", migration.Group, migration.Version)
					break
				}
			}
		}
		if !alreadyApplied {
			candidatesPerGroup := groups[migration.Group]
			candidatesPerGroup = append(candidatesPerGroup, migration)
			groups[migration.Group] = candidatesPerGroup
		}
	}

	// uniqueness check
	for _, candidates := range groups {
		sort.Slice(candidates, func(i, j int) bool {
			return candidates[i].Version < candidates[j].Version
		})
		// ensure unique constraints per group
		strictMonotonicVersion := int64(-1)
		for _, m := range candidates {
			if m.Version <= strictMonotonicVersion {
				return fmt.Errorf("the version must be >=0 and unique: %v", m)
			}
		}
	}

	// actually apply the missing migrations
	for _, candidates := range groups {
		for _, migration := range candidates {
			entry := HistoryEntry{
				Group:             migration.Group,
				Version:           migration.Version,
				Script:            migration.ScriptName,
				Type:              SQL,
				Checksum:          hash(migration),
				AppliedAt:         time.Now(),
				ExecutionDuration: 0,
				Status:            Executing,
			}

			start := time.Now()
			if err := insert(dbType, db, entry); err != nil {
				return fmt.Errorf("failed to insert history entry: %w", err)
			}

			if err := execute(db, migration); err != nil {
				entry.Log = err.Error()
				entry.Status = Failed
				_ = update(dbType, db, entry)
				return fmt.Errorf("failed to execute migration %s.%d: %w", migration.Group, migration.Version, err)
			}

			entry.Status = Success
			entry.ExecutionDuration = time.Now().Sub(start)

			if err := update(dbType, db, entry); err != nil {
				return fmt.Errorf("failed to update history migration: %w", err)
			}
		}
	}
	return nil
}

func version(tx DB) (DBType, error) {
	rows, err := tx.Query("SELECT version()")
	if err != nil {
		return "", err
	}

	// e.g. PostgreSQL 12.2 on x86_64-apple-darwin19.4.0, compiled by Apple clang version 11.0.3 (clang-1103.0.32.59), 64-bit
	// e.g. 10.4.11-MariaDB
	var str string
	for rows.Next() {
		if err := rows.Scan(&str); err != nil {
			return "", err
		}
	}
	str = strings.ToLower(str)
	if strings.Contains(str, "postgresql") {
		return PostgreSQL, nil
	}

	if strings.Contains(str, "mariadb") {
		return MySQL, nil
	}

	if strings.Contains(str, "mysql") {
		return MySQL, nil
	}

	return "", fmt.Errorf("unknown database type: %s", str)
}

func insert(dbtype DBType, tx DB, entry HistoryEntry) error {
	var stmt string
	switch dbtype {
	case MySQL:
		stmt = `INSERT INTO "migration_schema_history" ("group", "version", "script", "type", "checksum", "applied_at", "execution_duration", "status", "log") VALUES (?,?,?,?,?,?,?,?,?)`
	case PostgreSQL:
		stmt = `INSERT INTO "migration_schema_history" ("group", "version", "script", "type", "checksum", "applied_at", "execution_duration", "status", "log") VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)`
	}
	if _, err := tx.Exec(stmt, entry.Group, entry.Version, entry.Script, entry.Type, entry.Checksum, entry.AppliedAt, entry.ExecutionDuration, entry.Status, entry.Log); err != nil {
		return err
	}
	return nil
}

func update(dbtype DBType, tx DB, entry HistoryEntry) error {
	var stmt string
	switch dbtype {
	case MySQL:
		stmt = `UPDATE "migration_schema_history" SET "script"=?, "type"=?, "checksum"=?, "applied_at"=?, "execution_duration"=?, "status"=?, "log"=? WHERE "group"=? and "version"=?`
	case PostgreSQL:
		stmt = `UPDATE migration_schema_history SET "script"=$1, "type"=$2, "checksum"=$3, "applied_at"=$4, "execution_duration"=$5, "status"=$6, log=$7 WHERE "group"=$8 and "version"=$9`
	}
	if _, err := tx.Exec(stmt, entry.Script, entry.Type, entry.Checksum, entry.AppliedAt, entry.ExecutionDuration, entry.Status, entry.Log, entry.Group, entry.Version); err != nil {
		return err
	}
	return nil
}

func execute(tx DB, migration Migration) error {
	for _, stmt := range migration.Statements {
		if _, err := tx.Exec(stmt); err != nil {
			return fmt.Errorf("failed to execute statement '%s': %w", stmt, err)
		}
	}
	return nil
}

func CreateTable(tx DB) error {
	_, err := tx.Exec(createMigrationTable)
	return err
}

func History(tx DB) ([]HistoryEntry, error) {
	rows, err := tx.Query(`SELECT "group", "version", "script", "type", "checksum", "applied_at", "execution_duration", "status","log" FROM "migration_schema_history"`)
	if err != nil {
		return nil, fmt.Errorf("cannot select history: %w", err)
	}
	defer rows.Close()

	var res []HistoryEntry
	for rows.Next() {
		entry := HistoryEntry{}
		err = rows.Scan(&entry.Group, &entry.Version, &entry.Script, &entry.Type, &entry.Checksum, &entry.AppliedAt, &entry.ExecutionDuration, &entry.Status, &entry.Log)
		if err != nil {
			return res, fmt.Errorf("cannot scan entry: %w", err)
		}
		res = append(res, entry)
	}
	if rows.Err() != nil {
		return res, fmt.Errorf("cannot scan history: %w", err)
	}
	return res, nil
}
