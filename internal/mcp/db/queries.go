package db

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

func rowsToMaps(rows pgx.Rows) ([]map[string]any, error) {
	defer rows.Close()
	fields := rows.FieldDescriptions()
	var results []map[string]any
	for rows.Next() {
		vals, err := rows.Values()
		if err != nil {
			return nil, err
		}
		row := make(map[string]any, len(fields))
		for i, f := range fields {
			row[string(f.Name)] = vals[i]
		}
		results = append(results, row)
	}
	return results, rows.Err()
}

// QueryUser returns the user's records from user_accounts_source, user_accounts_camp,
// and account_sites filtered by email and optionally by trial aliases and/or site_ref.
func QueryUser(ctx context.Context, pool *pgxpool.Pool, email string, trials []string, siteRef string) (map[string]any, error) {
	result := map[string]any{}

	// ── account_sites ──────────────────────────────────────────────────
	var sitesRows pgx.Rows
	var err error
	if siteRef != "" {
		sitesRows, err = pool.Query(ctx, `
			SELECT id, account_id, site_reference_number, first_name, last_name,
			       email_address, lilly_email_address, site_role, site_status,
			       phone_number, is_active, country_code, source_id, is_active_top_role
			FROM accma.account_sites
			WHERE lower(email_address) = lower($1)
			  AND site_reference_number = $2`, email, siteRef)
	} else {
		sitesRows, err = pool.Query(ctx, `
			SELECT id, account_id, site_reference_number, first_name, last_name,
			       email_address, lilly_email_address, site_role, site_status,
			       phone_number, is_active, country_code, source_id, is_active_top_role
			FROM accma.account_sites
			WHERE lower(email_address) = lower($1)`, email)
	}
	if err != nil {
		return nil, fmt.Errorf("account_sites query: %w", err)
	}
	sites, err := rowsToMaps(sitesRows)
	if err != nil {
		return nil, fmt.Errorf("account_sites scan: %w", err)
	}
	result["account_sites"] = sites

	// ── user_accounts_camp ────────────────────────────────────────────
	campRows, err := pool.Query(ctx, `
		SELECT DISTINCT uac.account_id, uac.eph_personnel_id
		FROM accma.user_accounts_camp uac
		JOIN accma.account_sites asites ON asites.account_id = uac.account_id
		WHERE lower(asites.email_address) = lower($1)
		LIMIT 10`, email)
	if err != nil {
		return nil, fmt.Errorf("user_accounts_camp query: %w", err)
	}
	camp, err := rowsToMaps(campRows)
	if err != nil {
		return nil, fmt.Errorf("user_accounts_camp scan: %w", err)
	}
	result["camp_accounts"] = camp

	// ── user_accounts_source ──────────────────────────────────────────
	if len(trials) > 0 {
		placeholders := make([]string, len(trials))
		args := []any{email}
		for i, t := range trials {
			args = append(args, t)
			placeholders[i] = fmt.Sprintf("$%d", i+2)
		}
		q := fmt.Sprintf(`
			SELECT id, eph_personnel_id, first_name, last_name, email_address,
			       study_alias, site_reference_number, site_role, site_status,
			       phone_number, source_id, imported_date, updated_date
			FROM accma.user_accounts_source
			WHERE lower(email_address) = lower($1)
			  AND study_alias IN (%s)`, strings.Join(placeholders, ","))
		srcRows, err := pool.Query(ctx, q, args...)
		if err != nil {
			return nil, fmt.Errorf("user_accounts_source query: %w", err)
		}
		src, err := rowsToMaps(srcRows)
		if err != nil {
			return nil, fmt.Errorf("user_accounts_source scan: %w", err)
		}
		result["source_data"] = src
	} else {
		srcRows, err := pool.Query(ctx, `
			SELECT id, eph_personnel_id, first_name, last_name, email_address,
			       study_alias, site_reference_number, site_role, site_status,
			       phone_number, source_id, imported_date, updated_date
			FROM accma.user_accounts_source
			WHERE lower(email_address) = lower($1)
			LIMIT 50`, email)
		if err != nil {
			return nil, fmt.Errorf("user_accounts_source query: %w", err)
		}
		src, err := rowsToMaps(srcRows)
		if err != nil {
			return nil, fmt.Errorf("user_accounts_source scan: %w", err)
		}
		result["source_data"] = src
	}

	return result, nil
}

// GetErrorRecords returns accounts_system_errors for a user (identified by email + optional site_ref).
// Records with error_status_id=403 (IGNORED) are flagged — these block materialized view visibility.
func GetErrorRecords(ctx context.Context, pool *pgxpool.Pool, email, siteRef string) ([]map[string]any, error) {
	q := `
		SELECT ase.account_error_id, ase.account_id, ase.account_sites_id,
		       ase.error_type_id, ase.system_id, ase.error_status_id,
		       ase.study_id, ase.reported_date, ase.resolved_date,
		       ase.is_internally_fixed,
		       ses.error_status,
		       set2.error_type_code, set2.display_name AS error_display_name,
		       s.study_alias,
		       sys.system_name,
		       CASE WHEN ase.error_status_id = 403 THEN true ELSE false END AS is_ignored_blocking
		FROM accma.accounts_system_errors ase
		JOIN accma.system_error_status ses ON ses.error_status_id = ase.error_status_id
		JOIN accma.system_error_types set2 ON set2.error_type_id = ase.error_type_id
		JOIN accma.study s ON s.id = ase.study_id
		JOIN accma.systems sys ON sys.system_id = ase.system_id
		WHERE ase.account_sites_id IN (
			SELECT id FROM accma.account_sites
			WHERE lower(email_address) = lower($1)`
	args := []any{email}
	if siteRef != "" {
		q += " AND site_reference_number = $2"
		args = append(args, siteRef)
	}
	q += ") ORDER BY ase.study_id, ase.error_status_id"
	rows, err := pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("error records query: %w", err)
	}
	return rowsToMaps(rows)
}

// GetSystemStatus returns accounts_system_status for a user.
// systemID: 101=Veeva, 102=IWRS, 0=all systems.
func GetSystemStatus(ctx context.Context, pool *pgxpool.Pool, email, siteRef string, systemID int) ([]map[string]any, error) {
	siteSubquery := `SELECT id FROM accma.account_sites WHERE lower(email_address) = lower($1)`
	args := []any{email}

	var extra string
	if siteRef != "" {
		siteSubquery += " AND site_reference_number = $2"
		args = append(args, siteRef)
	}
	if systemID != 0 {
		args = append(args, systemID)
		extra = fmt.Sprintf("AND ass.system_id = $%d", len(args))
	}

	q := fmt.Sprintf(`
		SELECT ass.account_system_status_id, ass.account_sites_id, ass.study_id,
		       ass.system_id, ass.status_type_id, ass.system_role_id,
		       ass.requested_date, ass.completed_date, ass.deactivated_date,
		       ass.training_status_type_id,
		       ast.status, ast.display_name AS status_display, ast.show_in_ui,
		       s.study_alias, sys.system_name,
		       sr.system_role_name
		FROM accma.accounts_system_status ass
		JOIN accma.account_status_types ast ON ast.status_type_id = ass.status_type_id
		JOIN accma.study s ON s.id = ass.study_id
		JOIN accma.systems sys ON sys.system_id = ass.system_id
		LEFT JOIN accma.system_roles sr ON sr.id = ass.system_role_id AND sr.system_id = ass.system_id
		WHERE ass.account_sites_id IN (%s)
		%s
		ORDER BY sys.system_name, s.study_alias`, siteSubquery, extra)

	rows, err := pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("system status query: %w", err)
	}
	return rowsToMaps(rows)
}

// CheckDashboardVisibility queries the materialized view for a user.
func CheckDashboardVisibility(ctx context.Context, pool *pgxpool.Pool, email string) ([]map[string]any, error) {
	rows, err := pool.Query(ctx, `
		SELECT account_sites_id, account_status, account_id, first_name, last_name,
		       status_type_id, email_address, system, system_display_name,
		       site_name, site_role, study_id, study_name, description, training_status
		FROM accma.success_dashboard_account_list_view
		WHERE lower(email_address) = lower($1)
		ORDER BY system, study_name`, email)
	if err != nil {
		return nil, fmt.Errorf("dashboard visibility query: %w", err)
	}
	return rowsToMaps(rows)
}

// GetEctsInfo returns ects_info records and simulates the LEFT JOIN match conditions
// used by the materialized view to correlate external IWRS data with account_sites.
func GetEctsInfo(ctx context.Context, pool *pgxpool.Pool, email, siteRef string) ([]map[string]any, error) {
	q := `
		SELECT
			ects.study_alias, ects.site_reference_number,
			ects.first_name, ects.last_name, ects.email_address,
			ects.study_role, ects.iwrs_profile,
			ects.study_site_level_account_status, ects.phone,
			asites.id AS account_sites_id,
			asites.first_name AS sites_first_name,
			asites.last_name AS sites_last_name,
			COALESCE(asites.lilly_email_address, asites.email_address) AS sites_email,
			asites.phone_number AS sites_phone,
			(lower(ects.first_name) = lower(asites.first_name)) AS first_name_match,
			(lower(ects.last_name) = lower(asites.last_name)) AS last_name_match,
			(lower(ects.email_address) = lower(COALESCE(asites.lilly_email_address, asites.email_address))) AS email_match,
			(NOT ects.phone::text IS DISTINCT FROM asites.phone_number::text) AS phone_match
		FROM accma.ects_info ects
		LEFT JOIN accma.account_sites asites
			ON asites.site_reference_number = ects.site_reference_number
			AND lower(asites.email_address) = lower($1)
		WHERE lower(ects.email_address) = lower($1)`
	args := []any{email}
	if siteRef != "" {
		q += " AND ects.site_reference_number = $2"
		args = append(args, siteRef)
	}
	rows, err := pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("ects_info query: %w", err)
	}
	return rowsToMaps(rows)
}

// GetStudyConfig returns study + study_meta_data for the given trial aliases.
func GetStudyConfig(ctx context.Context, pool *pgxpool.Pool, trials []string) ([]map[string]any, error) {
	if len(trials) == 0 {
		return nil, fmt.Errorf("at least one trial alias required")
	}
	placeholders := make([]string, len(trials))
	args := make([]any, len(trials))
	for i, t := range trials {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		args[i] = t
	}
	q := fmt.Sprintf(`
		SELECT s.id AS study_id, s.study_alias, s.study_number, s.study_desc,
		       s.study_phase, s.study_status, s.is_active,
		       smd.study_meta_data_id, smd.system_id, smd.is_active AS system_active,
		       sys.system_name
		FROM accma.study s
		LEFT JOIN accma.study_meta_data smd ON smd.study_id = s.id
		LEFT JOIN accma.systems sys ON sys.system_id = smd.system_id
		WHERE s.study_alias IN (%s)
		ORDER BY s.study_alias, sys.system_name`, strings.Join(placeholders, ","))

	rows, err := pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("study config query: %w", err)
	}
	return rowsToMaps(rows)
}

func DescribeSchema(ctx context.Context, pool *pgxpool.Pool, tableName string) ([]map[string]any, error) {
	table := strings.TrimPrefix(tableName, "accma.")

	// Try information_schema first (works for tables and regular views).
	rows, err := pool.Query(ctx, `
		SELECT column_name, data_type, is_nullable, column_default
		FROM information_schema.columns
		WHERE table_schema = 'accma'
		  AND table_name = $1
		ORDER BY ordinal_position`, table)
	if err != nil {
		return nil, fmt.Errorf("describe schema query: %w", err)
	}
	result, err := rowsToMaps(rows)
	if err != nil {
		return nil, fmt.Errorf("describe schema scan: %w", err)
	}
	if len(result) > 0 {
		return result, nil
	}

	// Fallback to pg_catalog for materialized views (not in information_schema).
	mvRows, err := pool.Query(ctx, `
		SELECT a.attname AS column_name,
		       pg_catalog.format_type(a.atttypid, a.atttypmod) AS data_type,
		       CASE WHEN a.attnotnull THEN 'NO' ELSE 'YES' END AS is_nullable,
		       pg_get_expr(d.adbin, d.adrelid) AS column_default
		FROM pg_catalog.pg_attribute a
		JOIN pg_catalog.pg_class c ON c.oid = a.attrelid
		JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
		LEFT JOIN pg_catalog.pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
		WHERE n.nspname = 'accma'
		  AND c.relname = $1
		  AND a.attnum > 0
		  AND NOT a.attisdropped
		ORDER BY a.attnum`, table)
	if err != nil {
		return nil, fmt.Errorf("describe schema (pg_catalog) query: %w", err)
	}
	return rowsToMaps(mvRows)
}

func RunSafeQuery(ctx context.Context, pool *pgxpool.Pool, query string) ([]map[string]any, error) {
	// Execute in a read-only transaction — the database rejects any mutation.
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, fmt.Errorf("begin read-only tx: %w", err)
	}
	defer tx.Rollback(ctx)

	rows, err := tx.Query(ctx, query+" LIMIT 200")
	if err != nil {
		return nil, fmt.Errorf("query error: %w", err)
	}
	return rowsToMaps(rows)
}

// ── Live schema introspection ─────────────────────────────────────────────────

// TableSchema holds the full structure of one accma table, view, or materialized view.
type TableSchema struct {
	ObjectType  string       `json:"object_type"` // TABLE / MATERIALIZED_VIEW / VIEW
	Columns     []ColumnInfo `json:"columns"`
	PrimaryKey  []string     `json:"primary_key,omitempty"`
	ForeignKeys []FKInfo     `json:"foreign_keys,omitempty"`
	Indexes     []IndexInfo  `json:"indexes,omitempty"`
}

// ColumnInfo describes one column.
type ColumnInfo struct {
	OrdinalPosition int    `json:"ordinal_position"`
	ColumnName      string `json:"column_name"`
	DataType        string `json:"data_type"`
	IsNullable      string `json:"is_nullable"` // "YES" or "NO"
	ColumnDefault   string `json:"column_default,omitempty"`
}

// FKInfo describes one foreign-key relationship.
type FKInfo struct {
	ConstraintName   string `json:"constraint_name"`
	SourceColumn     string `json:"source_column"`
	ReferencedTable  string `json:"referenced_table"`
	ReferencedColumn string `json:"referenced_column"`
}

// IndexInfo describes one index.
type IndexInfo struct {
	IndexName       string `json:"index_name"`
	IsUnique        bool   `json:"is_unique"`
	IndexDefinition string `json:"index_definition"`
}

// GetLiveSchema introspects the accma schema directly from pg_catalog.
// tableFilter is optional; if non-empty, only that table/view is returned.
func GetLiveSchema(ctx context.Context, pool *pgxpool.Pool, tableFilter string) (map[string]TableSchema, error) {
	result := make(map[string]TableSchema)

	// ── Query 1: columns ─────────────────────────────────────────────────────
	colSQL := `
		SELECT
		    c.relname::text AS table_name,
		    CASE c.relkind
		        WHEN 'r' THEN 'TABLE'
		        WHEN 'm' THEN 'MATERIALIZED_VIEW'
		        WHEN 'v' THEN 'VIEW'
		    END::text AS object_type,
		    a.attnum::int AS ordinal_position,
		    a.attname::text AS column_name,
		    pg_catalog.format_type(a.atttypid, a.atttypmod)::text AS data_type,
		    CASE WHEN a.attnotnull THEN 'NO' ELSE 'YES' END::text AS is_nullable,
		    pg_get_expr(d.adbin, d.adrelid)::text AS column_default
		FROM pg_catalog.pg_attribute a
		JOIN pg_catalog.pg_class c ON c.oid = a.attrelid
		JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
		LEFT JOIN pg_catalog.pg_attrdef d ON d.adrelid = a.attrelid AND d.adnum = a.attnum
		WHERE n.nspname = 'accma'
		  AND a.attnum > 0
		  AND NOT a.attisdropped
		  AND c.relkind IN ('r', 'm', 'v')`
	colArgs := []any{}
	if tableFilter != "" {
		colArgs = append(colArgs, tableFilter)
		colSQL += fmt.Sprintf("\n  AND c.relname = $%d", len(colArgs))
	}
	colSQL += "\nORDER BY c.relname, a.attnum"

	colRows, err := pool.Query(ctx, colSQL, colArgs...)
	if err != nil {
		return nil, fmt.Errorf("schema columns query: %w", err)
	}
	defer colRows.Close()
	for colRows.Next() {
		var tableName, objectType, columnName, dataType, isNullable string
		var ordinal int
		var columnDefault *string
		if err := colRows.Scan(&tableName, &objectType, &ordinal, &columnName, &dataType, &isNullable, &columnDefault); err != nil {
			return nil, fmt.Errorf("schema columns scan: %w", err)
		}
		ts := result[tableName]
		ts.ObjectType = objectType
		col := ColumnInfo{
			OrdinalPosition: ordinal,
			ColumnName:      columnName,
			DataType:        dataType,
			IsNullable:      isNullable,
		}
		if columnDefault != nil {
			col.ColumnDefault = *columnDefault
		}
		ts.Columns = append(ts.Columns, col)
		result[tableName] = ts
	}
	if err := colRows.Err(); err != nil {
		return nil, fmt.Errorf("schema columns iterate: %w", err)
	}

	// ── Query 2: primary keys ─────────────────────────────────────────────────
	pkSQL := `
		SELECT
		    t.relname::text AS table_name,
		    a.attname::text AS column_name
		FROM pg_catalog.pg_class t
		JOIN pg_catalog.pg_namespace n ON n.oid = t.relnamespace
		JOIN pg_catalog.pg_index i ON i.indrelid = t.oid AND i.indisprimary
		JOIN pg_catalog.pg_attribute a ON a.attrelid = t.oid AND a.attnum = ANY(i.indkey)
		WHERE n.nspname = 'accma'`
	pkArgs := []any{}
	if tableFilter != "" {
		pkArgs = append(pkArgs, tableFilter)
		pkSQL += fmt.Sprintf("\n  AND t.relname = $%d", len(pkArgs))
	}
	pkSQL += "\nORDER BY t.relname, a.attnum"

	pkRows, err := pool.Query(ctx, pkSQL, pkArgs...)
	if err != nil {
		return nil, fmt.Errorf("schema PKs query: %w", err)
	}
	defer pkRows.Close()
	for pkRows.Next() {
		var tableName, colName string
		if err := pkRows.Scan(&tableName, &colName); err != nil {
			return nil, fmt.Errorf("schema PKs scan: %w", err)
		}
		ts := result[tableName]
		ts.PrimaryKey = append(ts.PrimaryKey, colName)
		result[tableName] = ts
	}
	if err := pkRows.Err(); err != nil {
		return nil, fmt.Errorf("schema PKs iterate: %w", err)
	}

	// ── Query 3: foreign keys ─────────────────────────────────────────────────
	fkSQL := `
		SELECT
		    t1.relname::text AS source_table,
		    a1.attname::text AS source_column,
		    t2.relname::text AS referenced_table,
		    a2.attname::text AS referenced_column,
		    c.conname::text  AS constraint_name
		FROM pg_catalog.pg_constraint c
		JOIN pg_catalog.pg_class t1 ON c.conrelid = t1.oid
		JOIN pg_catalog.pg_class t2 ON c.confrelid = t2.oid
		JOIN pg_catalog.pg_namespace n ON n.oid = t1.relnamespace
		JOIN pg_catalog.pg_attribute a1 ON a1.attrelid = t1.oid AND a1.attnum = ANY(c.conkey)
		JOIN pg_catalog.pg_attribute a2 ON a2.attrelid = t2.oid AND a2.attnum = ANY(c.confkey)
		WHERE c.contype = 'f'
		  AND n.nspname = 'accma'`
	fkArgs := []any{}
	if tableFilter != "" {
		fkArgs = append(fkArgs, tableFilter)
		fkSQL += fmt.Sprintf("\n  AND t1.relname = $%d", len(fkArgs))
	}
	fkSQL += "\nORDER BY t1.relname, c.conname"

	fkRows, err := pool.Query(ctx, fkSQL, fkArgs...)
	if err != nil {
		return nil, fmt.Errorf("schema FKs query: %w", err)
	}
	defer fkRows.Close()
	for fkRows.Next() {
		var sourceTable, sourceCol, refTable, refCol, constraintName string
		if err := fkRows.Scan(&sourceTable, &sourceCol, &refTable, &refCol, &constraintName); err != nil {
			return nil, fmt.Errorf("schema FKs scan: %w", err)
		}
		ts := result[sourceTable]
		ts.ForeignKeys = append(ts.ForeignKeys, FKInfo{
			ConstraintName:   constraintName,
			SourceColumn:     sourceCol,
			ReferencedTable:  refTable,
			ReferencedColumn: refCol,
		})
		result[sourceTable] = ts
	}
	if err := fkRows.Err(); err != nil {
		return nil, fmt.Errorf("schema FKs iterate: %w", err)
	}

	// ── Query 4: indexes (non-PK) ─────────────────────────────────────────────
	idxSQL := `
		SELECT
		    t.relname::text AS table_name,
		    i.relname::text AS index_name,
		    ix.indisunique::boolean AS is_unique,
		    pg_get_indexdef(ix.indexrelid)::text AS index_definition
		FROM pg_catalog.pg_class t
		JOIN pg_catalog.pg_namespace n ON n.oid = t.relnamespace
		JOIN pg_catalog.pg_index ix ON ix.indrelid = t.oid AND NOT ix.indisprimary
		JOIN pg_catalog.pg_class i ON i.oid = ix.indexrelid
		WHERE n.nspname = 'accma'
		  AND t.relkind = 'r'`
	idxArgs := []any{}
	if tableFilter != "" {
		idxArgs = append(idxArgs, tableFilter)
		idxSQL += fmt.Sprintf("\n  AND t.relname = $%d", len(idxArgs))
	}
	idxSQL += "\nORDER BY t.relname, i.relname"

	idxRows, err := pool.Query(ctx, idxSQL, idxArgs...)
	if err != nil {
		return nil, fmt.Errorf("schema indexes query: %w", err)
	}
	defer idxRows.Close()
	for idxRows.Next() {
		var tableName, indexName, indexDef string
		var isUnique bool
		if err := idxRows.Scan(&tableName, &indexName, &isUnique, &indexDef); err != nil {
			return nil, fmt.Errorf("schema indexes scan: %w", err)
		}
		ts := result[tableName]
		ts.Indexes = append(ts.Indexes, IndexInfo{
			IndexName:       indexName,
			IsUnique:        isUnique,
			IndexDefinition: indexDef,
		})
		result[tableName] = ts
	}
	if err := idxRows.Err(); err != nil {
		return nil, fmt.Errorf("schema indexes iterate: %w", err)
	}

	return result, nil
}
func GetProcessingHistory(ctx context.Context, pool *pgxpool.Pool, email, studyAlias string) ([]map[string]any, error) {
	baseQ := `
		SELECT d.id,
		       d.eph_personnel_id,
		       d.study_alias,
		       d.site_reference_number,
		       d.site_role,
		       d.operation,
		       d.is_existing_ephid,
		       d.timestamp,
		       d.change_details,
		       (SELECT string_agg(key, ', ' ORDER BY key)
		        FROM jsonb_object_keys(d.change_details -> 'delta') AS key
		       ) AS changed_fields
		FROM accma.camp_users_account_daily_delta d
		WHERE d.eph_personnel_id IN (
		    SELECT uac.eph_personnel_id
		    FROM accma.user_accounts_camp uac
		    JOIN accma.account_sites acs ON acs.account_id = uac.account_id
		    WHERE lower(acs.email_address) = lower($1)
		)`

	args := []any{email}
	if studyAlias != "" {
		args = append(args, studyAlias)
		baseQ += fmt.Sprintf("\nAND d.study_alias = $%d", len(args))
	}
	baseQ += "\nORDER BY d.timestamp DESC LIMIT 100"

	rows, err := pool.Query(ctx, baseQ, args...)
	if err != nil {
		return nil, fmt.Errorf("processing history query: %w", err)
	}
	return rowsToMaps(rows)
}

// GetSystemProcessingResult returns processing records for a user.
// For Veeva/ATOM5 it queries system_processed_result.
// For IWRS it queries the camp_ects_* staging tables.
// When system is empty, both are returned.
func GetSystemProcessingResult(ctx context.Context, pool *pgxpool.Pool, email, system, studyAlias string) (map[string]any, error) {
	result := map[string]any{}

	systemID := 0
	switch strings.ToLower(system) {
	case "veeva":
		systemID = 101
	case "atom5":
		systemID = 103
	}

	// ── system_processed_result (Veeva / ATOM5) ───────────────────────────
	runSPR := system == "" || system == "veeva" || system == "atom5"
	if runSPR {
		args := []any{email}
		extras := ""
		if systemID != 0 {
			args = append(args, systemID)
			extras += fmt.Sprintf(" AND spr.system_id = $%d", len(args))
		}
		if studyAlias != "" {
			args = append(args, studyAlias)
			extras += fmt.Sprintf(" AND s.study_alias = $%d", len(args))
		}
		q := fmt.Sprintf(`
			SELECT spr.id, spr.account_id, spr.system_id, spr.study_id, spr.batch_id,
			       spr.status, spr.comments, spr.email_sent_date, spr.requested_date,
			       spr.is_processed,
			       sys.system_name, s.study_alias
			FROM accma.system_processed_result spr
			JOIN accma.systems sys ON sys.system_id = spr.system_id
			JOIN accma.study s ON s.id = spr.study_id
			WHERE spr.account_id IN (
			    SELECT DISTINCT acs.account_id
			    FROM accma.account_sites acs
			    WHERE lower(acs.email_address) = lower($1)
			)%s
			ORDER BY spr.requested_date DESC LIMIT 100`, extras)

		sprRows, err := pool.Query(ctx, q, args...)
		if err != nil {
			return nil, fmt.Errorf("system_processed_result query: %w", err)
		}
		spr, err := rowsToMaps(sprRows)
		if err != nil {
			return nil, fmt.Errorf("system_processed_result scan: %w", err)
		}
		result["system_processed_result"] = spr
	}

	// ── IWRS staging tables ───────────────────────────────────────────────
	runIWRS := system == "" || strings.ToLower(system) == "iwrs"
	if runIWRS {
		args := []any{email}
		studyFilter := ""
		if studyAlias != "" {
			args = append(args, studyAlias)
			studyFilter = fmt.Sprintf("AND trial_alias = $%d", len(args))
		}
		q := fmt.Sprintf(`
			SELECT 'INSERT' AS action_type,
			       cei.source_id, cei.eph_id, cei.trial_alias AS study_alias,
			       cei.siteid AS site_reference_number, cei.first_name, cei.last_name,
			       cei.email_address, cei.study_role, cei.iwrs_profile,
			       cei.phone_number, cei.date_received, cei.is_uploaded_to_iwrs,
			       NULL::text AS updated_columns, NULL::text AS old_values
			FROM accma.camp_ects_insert cei
			WHERE lower(cei.email_address) = lower($1) %s
			UNION ALL
			SELECT 'UPDATE' AS action_type,
			       ceu.source_id, ceu.eph_id, ceu.trial_alias,
			       ceu.siteid, ceu.first_name, ceu.last_name,
			       ceu.email_address, ceu.study_role, ceu.iwrs_profile,
			       ceu.phone_number, ceu.date_received, ceu.is_uploaded_to_iwrs,
			       ceu.updated_columns, ceu.old_values
			FROM accma.camp_ects_update ceu
			WHERE lower(ceu.email_address) = lower($1) %s
			UNION ALL
			SELECT 'DEACTIVATE' AS action_type,
			       ced.source_id, ced.eph_id, ced.trial_alias,
			       ced.siteid, ced.first_name, ced.last_name,
			       ced.email_address, ced.study_role, ced.iwrs_profile,
			       ced.phone_number, ced.date_received, ced.is_uploaded_to_iwrs,
			       NULL::text, NULL::text
			FROM accma.camp_ects_deactivate ced
			WHERE lower(ced.email_address) = lower($1) %s
			ORDER BY date_received DESC LIMIT 100`,
			studyFilter, studyFilter, studyFilter)

		iwrsRows, err := pool.Query(ctx, q, args...)
		if err != nil {
			return nil, fmt.Errorf("iwrs staging query: %w", err)
		}
		iwrs, err := rowsToMaps(iwrsRows)
		if err != nil {
			return nil, fmt.Errorf("iwrs staging scan: %w", err)
		}
		result["iwrs_staging"] = iwrs
	}

	return result, nil
}

// GetRecentExceptions returns exception_audit_log entries within the last `days` days.
// Optionally filtered by a partial step_function_name match.
func GetRecentExceptions(ctx context.Context, pool *pgxpool.Pool, days int, stepFunctionFilter string) ([]map[string]any, error) {
	args := []any{days}
	extra := ""
	if stepFunctionFilter != "" {
		args = append(args, "%"+stepFunctionFilter+"%")
		extra = fmt.Sprintf("AND step_function_name ILIKE $%d", len(args))
	}
	q := fmt.Sprintf(`
		SELECT id, step_function_name, step_name, function_name,
		       error_desc, error_type, created_date
		FROM accma.exception_audit_log
		WHERE created_date >= NOW() - make_interval(days => $1)
		%s
		ORDER BY created_date DESC LIMIT 200`, extra)

	rows, err := pool.Query(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("exception audit log query: %w", err)
	}
	return rowsToMaps(rows)
}

// GetStatusHistory returns accounts_system_status_logs for a user.
// If that table does not exist (error code 42P01) it falls back to the current
// accounts_system_status and sets source="accounts_system_status_current".
func GetStatusHistory(ctx context.Context, pool *pgxpool.Pool, email string) (map[string]any, error) {
	rows, err := pool.Query(ctx, `
		SELECT assl.id,
		       assl.accounts_system_status_id,
		       assl.account_status_type_id,
		       assl.status_date,
		       assl.account_training_status_type_id,
		       ast.status       AS status,
		       ast.display_name AS status_display,
		       ass.study_id,
		       ass.system_id,
		       ass.account_sites_id,
		       s.study_alias,
		       sys.system_name
		FROM accma.accounts_system_status_logs assl
		JOIN accma.account_status_types ast ON ast.status_type_id = assl.account_status_type_id
		JOIN accma.accounts_system_status ass ON ass.account_system_status_id = assl.accounts_system_status_id
		JOIN accma.study s ON s.id = ass.study_id
		JOIN accma.systems sys ON sys.system_id = ass.system_id
		WHERE ass.account_sites_id IN (
		    SELECT id FROM accma.account_sites WHERE lower(email_address) = lower($1)
		)
		ORDER BY assl.status_date DESC LIMIT 100`, email)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "42P01" {
			// Table does not exist — fall back to current status snapshot.
			current, err2 := GetSystemStatus(ctx, pool, email, "", 0)
			if err2 != nil {
				return nil, err2
			}
			return map[string]any{
				"source":  "accounts_system_status_current",
				"records": current,
				"count":   len(current),
			}, nil
		}
		return nil, fmt.Errorf("status history query: %w", err)
	}
	records, err := rowsToMaps(rows)
	if err != nil {
		return nil, fmt.Errorf("status history scan: %w", err)
	}
	return map[string]any{
		"source":  "accounts_system_status_logs",
		"records": records,
		"count":   len(records),
	}, nil
}

// DiagnoseVisibilityResult holds the structured output of the diagnose_visibility tool.
type DiagnoseVisibilityResult struct {
	Email              string           `json:"email"`
	IsVisibleInView    bool             `json:"is_visible_in_view"`
	DashboardRows      []map[string]any `json:"dashboard_rows,omitempty"`
	AccountSites       []map[string]any `json:"account_sites"`
	AccountSitesActive bool             `json:"any_account_sites_active"`
	SystemStatuses     []map[string]any `json:"system_statuses"`
	ShowInUIFailing    []map[string]any `json:"show_in_ui_failing,omitempty"`
	BlockingErrors     []map[string]any `json:"blocking_errors_403,omitempty"`
	BlockingErrorCount int              `json:"blocking_error_count_403"`
	Studies            []map[string]any `json:"studies"`
	InactiveStudies    []string         `json:"inactive_studies,omitempty"`
	Verdict            string           `json:"verdict"`
	Reasons            []string         `json:"reasons"`
}

// DiagnoseVisibility runs the full "why isn't this user visible in the UI?" diagnostic chain.
func DiagnoseVisibility(ctx context.Context, pool *pgxpool.Pool, email string) (*DiagnoseVisibilityResult, error) {
	r := &DiagnoseVisibilityResult{Email: email}

	// Step 1 — materialized view
	dashRows, err := CheckDashboardVisibility(ctx, pool, email)
	if err != nil {
		return nil, fmt.Errorf("step1 (dashboard): %w", err)
	}
	r.DashboardRows = dashRows
	if len(dashRows) > 0 {
		r.IsVisibleInView = true
		r.Verdict = "VISIBLE"
		r.Reasons = []string{fmt.Sprintf("user appears in success_dashboard_account_list_view (%d row(s))", len(dashRows))}
		return r, nil
	}

	// Step 2 — account_sites existence + is_active
	siteRows, err := pool.Query(ctx, `
		SELECT id, account_id, site_reference_number, first_name, last_name,
		       email_address, lilly_email_address, site_role, site_status,
		       is_active, is_active_top_role, source_id
		FROM accma.account_sites
		WHERE lower(email_address) = lower($1)
		ORDER BY site_reference_number`, email)
	if err != nil {
		return nil, fmt.Errorf("step2 (account_sites): %w", err)
	}
	sites, err := rowsToMaps(siteRows)
	if err != nil {
		return nil, fmt.Errorf("step2 (account_sites scan): %w", err)
	}
	r.AccountSites = sites

	if len(sites) == 0 {
		r.Verdict = "NOT_VISIBLE"
		r.Reasons = append(r.Reasons, "user not found in account_sites — may not have been ingested yet")
		return r, nil
	}

	anyActive := false
	for _, s := range sites {
		if isActive, _ := s["is_active"].(bool); isActive {
			anyActive = true
		} else {
			r.Reasons = append(r.Reasons,
				fmt.Sprintf("account_sites.is_active=false for site_reference_number=%v (role=%v)",
					s["site_reference_number"], s["site_role"]))
		}
	}
	r.AccountSitesActive = anyActive

	// Step 3 — accounts_system_status + show_in_ui
	statusRows, err := pool.Query(ctx, `
		SELECT ass.account_system_status_id, ass.account_sites_id, ass.study_id,
		       ass.system_id, ass.status_type_id,
		       ass.requested_date, ass.completed_date, ass.deactivated_date,
		       ast.status, ast.display_name AS status_display, ast.show_in_ui,
		       s.study_alias, sys.system_name
		FROM accma.accounts_system_status ass
		JOIN accma.account_status_types ast ON ast.status_type_id = ass.status_type_id
		JOIN accma.study s ON s.id = ass.study_id
		JOIN accma.systems sys ON sys.system_id = ass.system_id
		WHERE ass.account_sites_id IN (
		    SELECT id FROM accma.account_sites WHERE lower(email_address) = lower($1)
		)
		ORDER BY sys.system_name, s.study_alias`, email)
	if err != nil {
		return nil, fmt.Errorf("step3 (system_status): %w", err)
	}
	statuses, err := rowsToMaps(statusRows)
	if err != nil {
		return nil, fmt.Errorf("step3 (system_status scan): %w", err)
	}
	r.SystemStatuses = statuses

	for _, s := range statuses {
		if showInUI, _ := s["show_in_ui"].(bool); !showInUI {
			r.ShowInUIFailing = append(r.ShowInUIFailing, s)
			r.Reasons = append(r.Reasons,
				fmt.Sprintf("status '%v' (id=%v) has show_in_ui=false for system=%v study=%v",
					s["status_display"], s["status_type_id"], s["system_name"], s["study_alias"]))
		}
	}

	// Step 4 — 403-IGNORED blocking errors
	errRows, err := GetErrorRecords(ctx, pool, email, "")
	if err != nil {
		return nil, fmt.Errorf("step4 (error_records): %w", err)
	}
	count := 0
	for _, e := range errRows {
		if blocking, _ := e["is_ignored_blocking"].(bool); blocking {
			r.BlockingErrors = append(r.BlockingErrors, e)
			count++
		}
	}
	r.BlockingErrorCount = count
	if count > 0 {
		r.Reasons = append(r.Reasons,
			fmt.Sprintf("%d IGNORED (error_status_id=403) error(s) — these block materialized view inclusion", count))
	}

	// Step 5 — study.is_active check
	studyRows, err := pool.Query(ctx, `
		SELECT DISTINCT s.id, s.study_alias, s.study_number, s.study_status, s.is_active,
		       smd.system_id, smd.is_active AS system_active,
		       sys.system_name
		FROM accma.account_sites acs
		JOIN accma.accounts_system_status ass ON ass.account_sites_id = acs.id
		JOIN accma.study s ON s.id = ass.study_id
		LEFT JOIN accma.study_meta_data smd ON smd.study_id = s.id
		LEFT JOIN accma.systems sys ON sys.system_id = smd.system_id
		WHERE lower(acs.email_address) = lower($1)
		ORDER BY s.study_alias, sys.system_name`, email)
	if err != nil {
		return nil, fmt.Errorf("step5 (study active): %w", err)
	}
	studies, err := rowsToMaps(studyRows)
	if err != nil {
		return nil, fmt.Errorf("step5 (study active scan): %w", err)
	}
	r.Studies = studies

	seenInactive := map[string]bool{}
	for _, s := range studies {
		alias, _ := s["study_alias"].(string)
		isActive, _ := s["is_active"].(bool)
		if !isActive && !seenInactive[alias] {
			seenInactive[alias] = true
			r.InactiveStudies = append(r.InactiveStudies, alias)
			r.Reasons = append(r.Reasons,
				fmt.Sprintf("study '%v' has is_active=false", alias))
		}
	}

	if len(r.Reasons) == 0 {
		r.Verdict = "INDETERMINATE"
		r.Reasons = append(r.Reasons,
			"all known checks passed (account_sites active, statuses show_in_ui, no blocking errors, studies active) — the materialized view may need a refresh, or there is an unmapped exclusion condition")
	} else {
		r.Verdict = "NOT_VISIBLE"
	}

	return r, nil
}

// SystemStatRow holds one row of the per-system stats breakdown.
type SystemStatRow struct {
	System      string `json:"system"`
	RequestType string `json:"request_type"`
	Count       int64  `json:"count"`
	IsUploaded  *bool  `json:"is_uploaded_to_iwrs,omitempty"` // IWRS only
}

// DailyStatsResult is returned by GetDailyStats.
type DailyStatsResult struct {
	Date   string           `json:"date"`
	Stats  []SystemStatRow  `json:"stats"`
	Totals map[string]int64 `json:"totals"`
}

// GetDailyStats returns activation/update/deactivation counts per system for a given date (YYYY-MM-DD).
//   - Veeva (101) and ATOM5 (103): counts from accounts_system_status JOIN account_status_types,
//     filtered by requested_date on that date.
//   - IWRS (102): counts from camp_ects_insert (activation), camp_ects_update (update),
//     camp_ects_deactivate (deactivation) filtered by date_received, broken down by is_uploaded_to_iwrs.
//
// If date is empty, defaults to today (CURRENT_DATE).
func GetDailyStats(ctx context.Context, pool *pgxpool.Pool, date string) (*DailyStatsResult, error) {
	if date == "" {
		date = "today"
	}

	var rows []SystemStatRow

	// ── Veeva + ATOM5 via accounts_system_status ──────────────────────────
	const veevaAtom5SQL = `
SELECT
    sys.system_name                                   AS system,
    ast.status                                        AS request_type,
    COUNT(*)                                          AS count
FROM accma.accounts_system_status ass
JOIN accma.systems sys ON sys.system_id = ass.system_id
JOIN accma.account_status_types ast ON ast.status_type_id = ass.status_type_id
WHERE ass.system_id IN (101, 103)
  AND ast.status IN ('NEW_REQUEST', 'UPDATE_REQUEST', 'DEACTIVATE_REQUEST')
  AND DATE(ass.requested_date) = $1::date
GROUP BY sys.system_name, ast.status
ORDER BY sys.system_name, ast.status
`
	dbRows, err := pool.Query(ctx, veevaAtom5SQL, date)
	if err != nil {
		return nil, fmt.Errorf("veeva/atom5 stats: %w", err)
	}
	vaMaps, err := rowsToMaps(dbRows)
	if err != nil {
		return nil, fmt.Errorf("veeva/atom5 stats scan: %w", err)
	}
	for _, m := range vaMaps {
		system, _ := m["system"].(string)
		reqType, _ := m["request_type"].(string)
		count, _ := m["count"].(int64)
		rows = append(rows, SystemStatRow{System: system, RequestType: reqType, Count: count})
	}

	// ── IWRS via camp_ects_* tables ───────────────────────────────────────
	const iwrsSQL = `
SELECT
    'IWRS'                        AS system,
    action_type,
    is_uploaded_to_iwrs,
    COUNT(*)                      AS count
FROM (
    SELECT 'NEW_REQUEST'       AS action_type, is_uploaded_to_iwrs FROM accma.camp_ects_insert    WHERE date_received = $1::date
    UNION ALL
    SELECT 'UPDATE_REQUEST'    AS action_type, is_uploaded_to_iwrs FROM accma.camp_ects_update    WHERE date_received = $1::date
    UNION ALL
    SELECT 'DEACTIVATE_REQUEST' AS action_type, is_uploaded_to_iwrs FROM accma.camp_ects_deactivate WHERE date_received = $1::date
) t
GROUP BY action_type, is_uploaded_to_iwrs
ORDER BY action_type, is_uploaded_to_iwrs
`
	iwrsDBRows, err := pool.Query(ctx, iwrsSQL, date)
	if err != nil {
		return nil, fmt.Errorf("iwrs stats: %w", err)
	}
	iwrsMaps, err := rowsToMaps(iwrsDBRows)
	if err != nil {
		return nil, fmt.Errorf("iwrs stats scan: %w", err)
	}
	for _, m := range iwrsMaps {
		reqType, _ := m["action_type"].(string)
		uploaded, _ := m["is_uploaded_to_iwrs"].(bool)
		count, _ := m["count"].(int64)
		u := uploaded
		rows = append(rows, SystemStatRow{System: "IWRS", RequestType: reqType, Count: count, IsUploaded: &u})
	}

	// ── Totals ────────────────────────────────────────────────────────────
	totals := map[string]int64{
		"NEW_REQUEST":        0,
		"UPDATE_REQUEST":     0,
		"DEACTIVATE_REQUEST": 0,
	}
	for _, r := range rows {
		if _, ok := totals[r.RequestType]; ok {
			totals[r.RequestType] += r.Count
		}
	}

	return &DailyStatsResult{Date: date, Stats: rows, Totals: totals}, nil
}
