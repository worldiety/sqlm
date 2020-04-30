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
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func scan(dir string) ([]migrationGroup, error) {
	var res []migrationGroup
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if info.Name() == "sqlm.json" {
			group := migrationGroup{file: path}
			b, err := ioutil.ReadFile(path)
			if err != nil {
				return err
			}
			err = json.Unmarshal(b, &group.config)
			if err != nil {
				return err
			}
			dialects, err := parseMigrations(group)
			if err != nil {
				return fmt.Errorf("failed to parse migrations: %w", err)
			}
			group.dialects = dialects
			res = append(res, group)
			//fmt.Printf("found %v\n", group)
		}
		return nil
	})
	return res, err
}

func parseMigrations(cfg migrationGroup) ([]dialect, error) {
	dir := filepath.Dir(cfg.file)
	var res []dialect
	for _, pkg := range cfg.config.Packages {
		dlc := dialect{pkg: pkg}
		schemaDir := filepath.Join(dir, pkg.Schema)
		fmt.Printf("reading schema dir %s\n", schemaDir)
		files, err := ioutil.ReadDir(schemaDir)
		if err != nil {
			return nil, err
		}
		for _, file := range files {
			if strings.HasSuffix(file.Name(), ".sql") {
				version := extractVersion(file.Name())
				if version == -1 {
					return nil, fmt.Errorf("invalid migration file name: %s", file.Name())
				}
				fmt.Printf("migration %s\n", file.Name())
				fname := filepath.Join(schemaDir, file.Name())
				stmts, err := parseStatements(fname)
				if err != nil {
					return nil, err
				}
				if len(stmts) == 0 {
					return nil, fmt.Errorf("migration file without statements: %s", fname)
				}
				fmt.Printf("   %d statements\n", len(stmts))
				migration := Migration{
					Group:      pkg.Group,
					Version:    version,
					Statements: stmts,
					ScriptName: file.Name(),
				}
				dlc.migrations = append(dlc.migrations, migration)
			}
		}
		res = append(res, dlc)
	}
	return res, nil
}

func extractVersion(name string) int64 {
	sb := &strings.Builder{}
	for _, r := range name {
		if r >= '0' && r <= '9' {
			sb.WriteRune(r)
		}
	}
	if sb.Len() == 0 {
		return -1
	}
	i, err := strconv.ParseInt(sb.String(), 10, 64)
	if err != nil {
		panic(sb.String())
	}
	return i
}

func parseStatements(fname string) ([]string, error) {
	b, err := ioutil.ReadFile(fname)
	if err != nil {
		return nil, err
	}

	stmts, err := parseStatementsFromString(string(b))
	if err != nil {
		return nil, fmt.Errorf("cannot parse %s: %w", fname, err)
	}
	return stmts, nil
}

func parseStatementsFromString(str string) ([]string, error) {
	var stmts []string
	rawStmts := commentsRegex.ReplaceAllString(str, " ")

	sb := &strings.Builder{}
	var lastRune rune
	for _, r := range rawStmts {
		if r == ';' {
			tmp := strings.TrimSpace(sb.String())
			if len(tmp) > 0 {
				stmts = append(stmts, tmp)
			}
			sb.Reset()
		} else {
			if r == '\r' || r == '\n' || r == '\t' {
				r = ' '
			}
			if lastRune == ' ' && r == ' ' {
				continue
			}
			lastRune = r
			sb.WriteRune(r)
		}
	}
	if len(strings.TrimSpace(sb.String())) > 0 {
		return nil, fmt.Errorf("non terminated sql statement")
	}
	return stmts, nil
}
