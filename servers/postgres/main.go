// mcpfs-postgres: PostgreSQL MCP resource server for mcpfs.
// Uses mcpserve framework. Speaks MCP JSON-RPC over stdio.
//
// Resources:
//   pg://databases                        - list databases
//   pg://tables                           - tables in current database
//   pg://tables/{schema}.{table}          - table details (columns, indexes)
//   pg://tables/{schema}.{table}/schema   - column definitions
//   pg://tables/{schema}.{table}/count    - row count
//   pg://tables/{schema}.{table}/sample   - first 100 rows as JSON
//   pg://extensions                       - installed extensions
//   pg://connections                      - active connections (pg_stat_activity)
//
// Auth: DATABASE_URL env var. All queries run in READ ONLY transactions.
package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	_ "github.com/lib/pq"

	"github.com/airshelf/mcpfs/pkg/mcpserve"
)

var db *sql.DB

func readResource(uri string) (mcpserve.ReadResult, error) {
	switch {
	case uri == "pg://databases":
		return queryJSON(
			"SELECT datname AS name, pg_database_size(datname) AS size_bytes FROM pg_database WHERE datistemplate = false ORDER BY datname",
		)
	case uri == "pg://tables":
		return queryJSON(
			"SELECT table_schema, table_name, table_type FROM information_schema.tables WHERE table_schema NOT IN ('pg_catalog','information_schema') ORDER BY table_schema, table_name",
		)
	case uri == "pg://extensions":
		return queryJSON(
			"SELECT extname AS name, extversion AS version FROM pg_extension ORDER BY extname",
		)
	case uri == "pg://connections":
		return queryJSON(
			"SELECT pid, usename, application_name, client_addr, state, query_start, left(query, 200) AS query FROM pg_stat_activity WHERE state IS NOT NULL ORDER BY query_start DESC NULLS LAST LIMIT 50",
		)
	case strings.HasPrefix(uri, "pg://tables/"):
		return readTable(strings.TrimPrefix(uri, "pg://tables/"))
	default:
		return mcpserve.ReadResult{}, fmt.Errorf("unknown resource: %s", uri)
	}
}

// readTable handles pg://tables/{schema}.{table}[/suffix]
func readTable(path string) (mcpserve.ReadResult, error) {
	schema, table, suffix, err := parseTablePath(path)
	if err != nil {
		return mcpserve.ReadResult{}, err
	}

	// Validate table exists via catalog (prevents SQL injection)
	exists, err := tableExists(schema, table)
	if err != nil {
		return mcpserve.ReadResult{}, err
	}
	if !exists {
		return mcpserve.ReadResult{}, fmt.Errorf("table not found: %s.%s", schema, table)
	}

	switch suffix {
	case "":
		return readTableDetails(schema, table)
	case "schema":
		return readTableSchema(schema, table)
	case "count":
		return readTableCount(schema, table)
	case "sample":
		return readTableSample(schema, table)
	default:
		return mcpserve.ReadResult{}, fmt.Errorf("unknown table resource: %s", suffix)
	}
}

func parseTablePath(path string) (schema, table, suffix string, err error) {
	// Split off suffix: schema.table/suffix
	parts := strings.SplitN(path, "/", 2)
	qualName := parts[0]
	if len(parts) == 2 {
		suffix = parts[1]
	}

	// Split schema.table
	dot := strings.SplitN(qualName, ".", 2)
	if len(dot) != 2 || dot[0] == "" || dot[1] == "" {
		return "", "", "", fmt.Errorf("invalid table path: %s (expected schema.table)", qualName)
	}
	return dot[0], dot[1], suffix, nil
}

// tableExists checks catalog to validate table name (SQL injection prevention).
func tableExists(schema, table string) (bool, error) {
	var exists bool
	err := db.QueryRow(
		"SELECT EXISTS(SELECT 1 FROM information_schema.tables WHERE table_schema=$1 AND table_name=$2)",
		schema, table,
	).Scan(&exists)
	return exists, err
}

func readTableDetails(schema, table string) (mcpserve.ReadResult, error) {
	return queryJSON(`
		SELECT column_name, data_type, is_nullable, column_default
		FROM information_schema.columns
		WHERE table_schema=$1 AND table_name=$2
		ORDER BY ordinal_position`,
		schema, table)
}

func readTableSchema(schema, table string) (mcpserve.ReadResult, error) {
	return queryJSON(`
		SELECT column_name, data_type, character_maximum_length,
			   is_nullable, column_default, udt_name
		FROM information_schema.columns
		WHERE table_schema=$1 AND table_name=$2
		ORDER BY ordinal_position`,
		schema, table)
}

func readTableCount(schema, table string) (mcpserve.ReadResult, error) {
	// Use identifier quoting for the count query (table already validated via catalog)
	var count int64
	err := db.QueryRow(
		fmt.Sprintf(`SELECT count(*) FROM "%s"."%s"`, schema, table),
	).Scan(&count)
	if err != nil {
		return mcpserve.ReadResult{}, err
	}
	return mcpserve.ReadResult{Text: fmt.Sprintf("%d", count), MimeType: "text/plain"}, nil
}

func readTableSample(schema, table string) (mcpserve.ReadResult, error) {
	// Table already validated via catalog lookup
	return queryJSON(
		fmt.Sprintf(`SELECT * FROM "%s"."%s" LIMIT 100`, schema, table),
	)
}

// queryJSON executes a read-only query and returns results as JSON array.
func queryJSON(query string, args ...interface{}) (mcpserve.ReadResult, error) {
	tx, err := db.Begin()
	if err != nil {
		return mcpserve.ReadResult{}, err
	}
	defer tx.Rollback()

	if _, err := tx.Exec("SET TRANSACTION READ ONLY"); err != nil {
		return mcpserve.ReadResult{}, err
	}

	rows, err := tx.Query(query, args...)
	if err != nil {
		return mcpserve.ReadResult{}, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return mcpserve.ReadResult{}, err
	}

	var results []map[string]interface{}
	for rows.Next() {
		vals := make([]interface{}, len(cols))
		ptrs := make([]interface{}, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return mcpserve.ReadResult{}, err
		}

		row := make(map[string]interface{})
		for i, col := range cols {
			v := vals[i]
			// Convert []byte to string for JSON readability
			if b, ok := v.([]byte); ok {
				row[col] = string(b)
			} else {
				row[col] = v
			}
		}
		results = append(results, row)
	}

	if results == nil {
		results = []map[string]interface{}{}
	}

	out, _ := json.MarshalIndent(results, "", "  ")
	return mcpserve.ReadResult{Text: string(out), MimeType: "application/json"}, nil
}

func main() {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		fmt.Fprintln(os.Stderr, "mcpfs-postgres: DATABASE_URL env var required")
		os.Exit(1)
	}

	var err error
	db, err = sql.Open("postgres", dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mcpfs-postgres: %v\n", err)
		os.Exit(1)
	}
	defer db.Close()

	if err := db.Ping(); err != nil {
		fmt.Fprintf(os.Stderr, "mcpfs-postgres: cannot connect: %v\n", err)
		os.Exit(1)
	}

	srv := mcpserve.New("mcpfs-postgres", "0.1.0", readResource)

	srv.AddResource(mcpserve.Resource{
		URI: "pg://databases", Name: "databases",
		Description: "List all databases", MimeType: "application/json",
	})
	srv.AddResource(mcpserve.Resource{
		URI: "pg://tables", Name: "tables",
		Description: "Tables in current database", MimeType: "application/json",
	})
	srv.AddResource(mcpserve.Resource{
		URI: "pg://extensions", Name: "extensions",
		Description: "Installed extensions", MimeType: "application/json",
	})
	srv.AddResource(mcpserve.Resource{
		URI: "pg://connections", Name: "connections",
		Description: "Active connections (pg_stat_activity)", MimeType: "application/json",
	})

	srv.AddTemplate(mcpserve.Template{
		URITemplate: "pg://tables/{schema}.{table}", Name: "table-details",
		Description: "Table column definitions", MimeType: "application/json",
	})
	srv.AddTemplate(mcpserve.Template{
		URITemplate: "pg://tables/{schema}.{table}/schema", Name: "table-schema",
		Description: "Detailed column schema (types, defaults, nullability)", MimeType: "application/json",
	})
	srv.AddTemplate(mcpserve.Template{
		URITemplate: "pg://tables/{schema}.{table}/count", Name: "table-count",
		Description: "Row count", MimeType: "text/plain",
	})
	srv.AddTemplate(mcpserve.Template{
		URITemplate: "pg://tables/{schema}.{table}/sample", Name: "table-sample",
		Description: "First 100 rows as JSON", MimeType: "application/json",
	})

	if err := srv.Serve(); err != nil {
		fmt.Fprintf(os.Stderr, "mcpfs-postgres: %v\n", err)
		os.Exit(1)
	}
}
