package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

func databaseCommandPlan(name string) ([]commandSpec, bool) {
	switch name {
	case "db:up":
		return []commandSpec{{
			executable: "docker", arguments: []string{"compose", "up", "-d", "--wait", "postgres"}, timeout: 5 * time.Minute,
		}}, true
	case "db:down":
		return []commandSpec{{executable: "docker", arguments: []string{"compose", "down"}, timeout: 2 * time.Minute}}, true
	case "db:reset":
		return []commandSpec{
			{executable: "docker", arguments: []string{"compose", "down", "-v"}, timeout: 2 * time.Minute},
			{executable: "docker", arguments: []string{"compose", "up", "-d", "--wait", "postgres"}, timeout: 5 * time.Minute},
		}, true
	case "db:status":
		return []commandSpec{{executable: "docker", arguments: []string{"compose", "ps", "postgres"}, timeout: 30 * time.Second}}, true
	case "migrate":
		return []commandSpec{{
			executable: "go",
			arguments:  []string{"test", "-count=1", "-tags=integration", "-timeout", "300s", "-run", "^TestPostgreSQLMigrations$", "./internal/adapters/postgres"},
			timeout:    6 * time.Minute,
		}}, true
	case "migrate:check":
		return []commandSpec{{
			executable: "go",
			arguments:  []string{"test", "-count=1", "-timeout", "60s", "-run", "^TestDiscoverMigrations", "./internal/adapters/postgres"},
			timeout:    90 * time.Second,
		}}, true
	case "graph:check":
		return []commandSpec{{
			executable: "go",
			arguments:  []string{"test", "-count=1", "-tags=integration", "-timeout", "300s", "-run", "^TestPostgreSQLMigrations$/policy_graph_", "./internal/adapters/postgres"},
			timeout:    6 * time.Minute,
		}}, true
	default:
		return nil, false
	}
}

func createMigration(deps dependencies, name string) error {
	if !validMigrationName(name) || deps.stdout == nil {
		return errors.New("devx: migration name must contain lowercase letters, digits, and underscores")
	}
	repository, err := dependencyRepositoryRoot(deps)
	if err != nil {
		return err
	}
	directory := filepath.Join(repository, "migrations")
	entries, err := os.ReadDir(directory)
	if err != nil {
		return err
	}
	version := 1
	for _, entry := range entries {
		filename := entry.Name()
		if entry.IsDir() || len(filename) < 7 || filename[6] != '_' {
			continue
		}
		value, parseErr := strconv.Atoi(filename[:6])
		if parseErr == nil && value >= version {
			version = value + 1
		}
	}
	prefix := fmt.Sprintf("%06d_%s", version, name)
	upPath := filepath.Join(directory, prefix+".up.sql")
	downPath := filepath.Join(directory, prefix+".down.sql")
	contents := []byte("BEGIN;\n\nCOMMIT;\n")
	if err := writeExclusiveFile(upPath, contents); err != nil {
		return err
	}
	if err := writeExclusiveFile(downPath, contents); err != nil {
		_ = os.Remove(upPath)
		return err
	}
	_, err = fmt.Fprintf(deps.stdout, "created migrations/%s.{up,down}.sql\n", prefix)
	return err
}

func validMigrationName(name string) bool {
	if name == "" || name[0] == '_' || name[len(name)-1] == '_' || strings.Contains(name, "__") {
		return false
	}
	for index := range len(name) {
		character := name[index]
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '_' {
			return false
		}
	}
	return true
}

func writeExclusiveFile(path string, contents []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	if _, err = file.Write(contents); err != nil {
		_ = file.Close()
		_ = os.Remove(path)
		return err
	}
	return file.Close()
}
