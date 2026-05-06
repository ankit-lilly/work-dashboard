package mcptools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/EliLillyCo/work-dashboard/internal/mcp/db"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func NewMCPServer(pool *db.Pool, schemaSQL string) *server.MCPServer {
	s := server.NewMCPServer(
		"CAMP Investigator",
		"1.0.0",
		server.WithToolCapabilities(true),
		server.WithResourceCapabilities(false, false),
		server.WithInstructions(campInstructions),
	)
	registerTools(s, pool, schemaSQL)
	registerResources(s)
	return s
}

func registerResources(s *server.MCPServer) {
	s.AddResource(
		mcp.NewResource("camp://data-model", "CAMP Data Model",
			mcp.WithResourceDescription("Complete schema and relationship documentation for the CAMP accma database"),
			mcp.WithMIMEType("text/plain"),
		),
		func(ctx context.Context, req mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			return []mcp.ResourceContents{
				mcp.TextResourceContents{
					URI:      "camp://data-model",
					MIMEType: "text/plain",
					Text:     campInstructions,
				},
			}, nil
		},
	)
}

func BuildPool() *db.Pool {
	raw := os.Getenv("MCP_ENVS")
	if raw == "" {
		raw = "dev:campdev:us-east-2,qa:campqa:us-east-2,prod:campprod:us-east-2"
	}
	secretTemplate := os.Getenv("MCP_SECRET_ID_TEMPLATE")
	if secretTemplate == "" {
		secretTemplate = "awsrds-gdm-aurora-pgsql-accma-%s-cluster"
	}

	envs := make(map[string]db.EnvInfo)
	for _, part := range strings.Split(raw, ",") {
		seg := strings.Split(strings.TrimSpace(part), ":")
		if len(seg) < 2 {
			continue
		}
		name := seg[0]
		profile := seg[1]
		region := "us-east-2"
		if len(seg) >= 3 {
			region = seg[2]
		}
		envs[name] = db.EnvInfo{Profile: profile, Region: region}
	}

	return db.NewPool(db.Config{
		SecretIDTemplate: secretTemplate,
		Envs:             envs,
	})
}

// ── Tool registration ─────────────────────────────────────────────────────────

func registerTools(s *server.MCPServer, pool *db.Pool, schemaSQL string) {
	s.AddTool(mcp.NewTool("list_environments",
		mcp.WithDescription("List all available environments and their AWS profiles."),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		return jsonResult(pool.ConfigSnapshot())
	})

	s.AddTool(mcp.NewTool("query_user",
		mcp.WithDescription("Look up a user by email across account_sites, user_accounts_camp, and user_accounts_source."),
		mcp.WithString("env", mcp.Required(), mcp.Description("Environment: dev, qa, or prod")),
		mcp.WithString("email", mcp.Required(), mcp.Description("User email address")),
		mcp.WithString("trials", mcp.Description("Comma-separated trial/study aliases to filter source data (e.g. N1T-MC-TZ01,N1T-MC-RT01). Optional.")),
		mcp.WithString("site_ref", mcp.Description("Site reference number to narrow account_sites results. Optional.")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		p, err := pool.Get(ctx, argStr(args, "env"))
		if err != nil {
			return errResult(err)
		}
		result, err := db.QueryUser(ctx, p, argStr(args, "email"), splitCSV(args["trials"]), argStr(args, "site_ref"))
		if err != nil {
			return errResult(err)
		}
		return jsonResult(result)
	})

	s.AddTool(mcp.NewTool("get_error_records",
		mcp.WithDescription("Get error records for a user. error_status_id=403 (IGNORED, flagged as is_ignored_blocking) blocks dashboard visibility."),
		mcp.WithString("env", mcp.Required(), mcp.Description("Environment: dev, qa, or prod")),
		mcp.WithString("email", mcp.Required(), mcp.Description("User email address")),
		mcp.WithString("site_ref", mcp.Description("Site reference number. Optional — omit to get errors across all sites.")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		p, err := pool.Get(ctx, argStr(args, "env"))
		if err != nil {
			return errResult(err)
		}
		rows, err := db.GetErrorRecords(ctx, p, argStr(args, "email"), argStr(args, "site_ref"))
		if err != nil {
			return errResult(err)
		}

		var ignored int
		for _, r := range rows {
			if b, ok := r["is_ignored_blocking"].(bool); ok && b {
				ignored++
			}
		}
		diag := "No blocking errors found."
		if ignored > 0 {
			diag = fmt.Sprintf(
				"%d IGNORED (error_status_id=403) error(s) found. "+
					"These trigger the NOT EXISTS clause in the materialized view and prevent the user from appearing on the CAMP dashboard. "+
					"Fix: update error_status_id from 403 to 401, then refresh the materialized view.",
				ignored,
			)
		}
		type response struct {
			Records      []map[string]any `json:"records"`
			TotalErrors  int              `json:"total_errors"`
			IgnoredCount int              `json:"ignored_count_403"`
			Diagnosis    string           `json:"diagnosis"`
		}
		return jsonResult(response{Records: rows, TotalErrors: len(rows), IgnoredCount: ignored, Diagnosis: diag})
	})

	s.AddTool(mcp.NewTool("get_system_status",
		mcp.WithDescription("Get accounts_system_status rows for a user. Shows status in IWRS (system_id=102), Veeva (system_id=101), or all systems."),
		mcp.WithString("env", mcp.Required(), mcp.Description("Environment: dev, qa, or prod")),
		mcp.WithString("email", mcp.Required(), mcp.Description("User email address")),
		mcp.WithString("site_ref", mcp.Description("Site reference number. Optional.")),
		mcp.WithString("system", mcp.Description("Filter by system: 'iwrs', 'veeva', or omit for all systems.")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		p, err := pool.Get(ctx, argStr(args, "env"))
		if err != nil {
			return errResult(err)
		}
		systemID := 0
		switch strings.ToLower(argStr(args, "system")) {
		case "iwrs":
			systemID = 102
		case "veeva":
			systemID = 101
		}
		rows, err := db.GetSystemStatus(ctx, p, argStr(args, "email"), argStr(args, "site_ref"), systemID)
		if err != nil {
			return errResult(err)
		}
		return jsonResult(rows)
	})

	s.AddTool(mcp.NewTool("check_dashboard_visibility",
		mcp.WithDescription("Query the success_dashboard_account_list_view materialized view to see which systems and studies a user is visible in on the CAMP dashboard."),
		mcp.WithString("env", mcp.Required(), mcp.Description("Environment: dev, qa, or prod")),
		mcp.WithString("email", mcp.Required(), mcp.Description("User email address")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		p, err := pool.Get(ctx, argStr(args, "env"))
		if err != nil {
			return errResult(err)
		}
		rows, err := db.CheckDashboardVisibility(ctx, p, argStr(args, "email"))
		if err != nil {
			return errResult(err)
		}
		bySystem := map[string]int{}
		for _, r := range rows {
			if sys, ok := r["system"].(string); ok {
				bySystem[sys]++
			}
		}
		type response struct {
			Rows     []map[string]any `json:"rows"`
			Total    int              `json:"total_rows"`
			BySystem map[string]int   `json:"by_system"`
		}
		return jsonResult(response{Rows: rows, Total: len(rows), BySystem: bySystem})
	})

	s.AddTool(mcp.NewTool("get_ects_info",
		mcp.WithDescription("Returns ects_info records for a user and simulates the LEFT JOIN match conditions (name/email/phone) used by the materialized view to correlate IWRS data. A failed phone_match is a common cause of incorrect status display."),
		mcp.WithString("env", mcp.Required(), mcp.Description("Environment: dev, qa, or prod")),
		mcp.WithString("email", mcp.Required(), mcp.Description("User email address")),
		mcp.WithString("site_ref", mcp.Description("Site reference number. Optional.")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		p, err := pool.Get(ctx, argStr(args, "env"))
		if err != nil {
			return errResult(err)
		}
		rows, err := db.GetEctsInfo(ctx, p, argStr(args, "email"), argStr(args, "site_ref"))
		if err != nil {
			return errResult(err)
		}
		return jsonResult(rows)
	})

	s.AddTool(mcp.NewTool("get_study_config",
		mcp.WithDescription("Returns study metadata and system configuration (IWRS/Veeva active status) for given trial aliases."),
		mcp.WithString("env", mcp.Required(), mcp.Description("Environment: dev, qa, or prod")),
		mcp.WithString("trials", mcp.Required(), mcp.Description("Comma-separated study/trial aliases (e.g. N1T-MC-TZ01,N1T-MC-RT01)")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		trials := splitCSV(args["trials"])
		if len(trials) == 0 {
			return errResult(fmt.Errorf("trials parameter is required"))
		}
		p, err := pool.Get(ctx, argStr(args, "env"))
		if err != nil {
			return errResult(err)
		}
		rows, err := db.GetStudyConfig(ctx, p, trials)
		if err != nil {
			return errResult(err)
		}
		return jsonResult(rows)
	})

	s.AddTool(mcp.NewTool("describe_schema",
		mcp.WithDescription("Quick column lookup for a table (names, types, nullability). Prefer get_live_schema for full structure including PKs, FKs, and indexes."),
		mcp.WithString("env", mcp.Required(), mcp.Description("Environment: dev, qa, or prod")),
		mcp.WithString("table", mcp.Required(), mcp.Description("Table name (with or without 'accma.' prefix, e.g. 'account_sites' or 'accma.account_sites')")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		p, err := pool.Get(ctx, argStr(args, "env"))
		if err != nil {
			return errResult(err)
		}
		rows, err := db.DescribeSchema(ctx, p, argStr(args, "table"))
		if err != nil {
			return errResult(err)
		}
		return jsonResult(rows)
	})

	s.AddTool(mcp.NewTool("run_safe_query",
		mcp.WithDescription("Execute a read-only SELECT against accma (max 200 rows). Use get_live_schema to verify column names first."),
		mcp.WithString("env", mcp.Required(), mcp.Description("Environment: dev, qa, or prod")),
		mcp.WithString("query", mcp.Required(), mcp.Description("A SELECT SQL query targeting accma.* tables")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		p, err := pool.Get(ctx, argStr(args, "env"))
		if err != nil {
			return errResult(err)
		}
		rows, err := db.RunSafeQuery(ctx, p, argStr(args, "query"))
		if err != nil {
			return errResult(err)
		}
		type response struct {
			Rows  []map[string]any `json:"rows"`
			Count int              `json:"count"`
		}
		return jsonResult(response{Rows: rows, Count: len(rows)})
	})

	s.AddTool(mcp.NewTool("diagnose_visibility",
		mcp.WithDescription("Diagnose why a user is or isn't visible in the CAMP dashboard. Returns a verdict (VISIBLE/NOT_VISIBLE/INDETERMINATE) with reasons. Checks: dashboard presence, is_active, show_in_ui, 403 errors, study active status."),
		mcp.WithString("env", mcp.Required(), mcp.Description("Environment: dev, qa, or prod")),
		mcp.WithString("email", mcp.Required(), mcp.Description("User email address to diagnose")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		p, err := pool.Get(ctx, argStr(args, "env"))
		if err != nil {
			return errResult(err)
		}
		result, err := db.DiagnoseVisibility(ctx, p, argStr(args, "email"))
		if err != nil {
			return errResult(err)
		}
		return jsonResult(result)
	})

	s.AddTool(mcp.NewTool("get_processing_history",
		mcp.WithDescription("Query camp_users_account_daily_delta for a user's processing history. Returns all INSERT/UPDATE/REPROCESS_FEDID operations with timestamps, operation type, and decoded JSONB change_details showing previous/updated values per field. The changed_fields column summarises which fields were affected."),
		mcp.WithString("env", mcp.Required(), mcp.Description("Environment: dev, qa, or prod")),
		mcp.WithString("email", mcp.Required(), mcp.Description("User email address")),
		mcp.WithString("study_alias", mcp.Description("Optional: filter to a specific study alias, e.g. N1T-MC-TZ01")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		p, err := pool.Get(ctx, argStr(args, "env"))
		if err != nil {
			return errResult(err)
		}
		rows, err := db.GetProcessingHistory(ctx, p, argStr(args, "email"), argStr(args, "study_alias"))
		if err != nil {
			return errResult(err)
		}
		type response struct {
			Records []map[string]any `json:"records"`
			Count   int              `json:"count"`
		}
		return jsonResult(response{Records: rows, Count: len(rows)})
	})

	s.AddTool(mcp.NewTool("get_system_processing_result",
		mcp.WithDescription("Query processing results per system for a user. For Veeva and ATOM5, queries system_processed_result (batch_id, status, comments, dates). For IWRS, queries the camp_ects_insert/update/deactivate staging tables showing what was sent to Oracle eCTS and whether it was uploaded (is_uploaded_to_iwrs). Response has separate 'system_processed_result' and 'iwrs_staging' keys."),
		mcp.WithString("env", mcp.Required(), mcp.Description("Environment: dev, qa, or prod")),
		mcp.WithString("email", mcp.Required(), mcp.Description("User email address")),
		mcp.WithString("system", mcp.Description("Optional: filter by system — 'veeva', 'iwrs', 'atom5', or omit for all")),
		mcp.WithString("study_alias", mcp.Description("Optional: filter to a specific study alias")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		p, err := pool.Get(ctx, argStr(args, "env"))
		if err != nil {
			return errResult(err)
		}
		result, err := db.GetSystemProcessingResult(ctx, p,
			argStr(args, "email"),
			argStr(args, "system"),
			argStr(args, "study_alias"),
		)
		if err != nil {
			return errResult(err)
		}
		return jsonResult(result)
	})

	s.AddTool(mcp.NewTool("get_recent_exceptions",
		mcp.WithDescription("Query exception_audit_log for recent step function and Lambda failures. Use this to check if the pipeline itself failed around the time a user was not updated. Defaults to last 7 days. Filterable by partial step_function_name match (e.g. 'veeva', 'iwrs', 'atom5')."),
		mcp.WithString("env", mcp.Required(), mcp.Description("Environment: dev, qa, or prod")),
		mcp.WithNumber("days", mcp.Description("Number of days to look back (default: 7, max: 90)")),
		mcp.WithString("step_function_filter", mcp.Description("Optional: partial match on step_function_name, e.g. 'veeva' or 'iwrs'")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		p, err := pool.Get(ctx, argStr(args, "env"))
		if err != nil {
			return errResult(err)
		}
		days := req.GetInt("days", 7)
		if days <= 0 || days > 90 {
			days = 7
		}
		rows, err := db.GetRecentExceptions(ctx, p, days, argStr(args, "step_function_filter"))
		if err != nil {
			return errResult(err)
		}
		type response struct {
			Records  []map[string]any `json:"records"`
			Count    int              `json:"count"`
			DaysBack int              `json:"days_back"`
		}
		return jsonResult(response{Records: rows, Count: len(rows), DaysBack: days})
	})

	s.AddTool(mcp.NewTool("get_status_history",
		mcp.WithDescription("Query accounts_system_status_logs for status transitions over time for a user. If that log table does not exist (some environments), automatically falls back to the current accounts_system_status snapshot. The response 'source' field indicates which was used."),
		mcp.WithString("env", mcp.Required(), mcp.Description("Environment: dev, qa, or prod")),
		mcp.WithString("email", mcp.Required(), mcp.Description("User email address")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		p, err := pool.Get(ctx, argStr(args, "env"))
		if err != nil {
			return errResult(err)
		}
		result, err := db.GetStatusHistory(ctx, p, argStr(args, "email"))
		if err != nil {
			return errResult(err)
		}
		return jsonResult(result)
	})

	s.AddTool(mcp.NewTool("get_live_schema",
		mcp.WithDescription("Return the live accma schema for one table or all tables — columns, types, nullability, defaults, PKs, FKs, and indexes from pg_catalog. Use this before run_safe_query. Falls back to embedded schema.sql when the DB is unreachable."),
		mcp.WithString("env", mcp.Required(), mcp.Description("Environment: dev, qa, or prod")),
		mcp.WithString("table", mcp.Description("Optional: restrict to one table/view name without the accma. prefix, e.g. 'user_accounts_camp'")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		tableFilter := strings.TrimPrefix(argStr(args, "table"), "accma.")

		p, poolErr := pool.Get(ctx, argStr(args, "env"))
		if poolErr == nil {
			schema, err := db.GetLiveSchema(ctx, p, tableFilter)
			if err == nil {
				type liveResponse struct {
					Source string                    `json:"source"`
					Schema map[string]db.TableSchema `json:"schema"`
				}
				return jsonResult(liveResponse{Source: "live", Schema: schema})
			}
			poolErr = err
		}

		// Fallback: return the embedded schema.sql snapshot.
		type fallbackResponse struct {
			Source string `json:"source"`
			Note   string `json:"note"`
			Error  string `json:"error"`
			Schema string `json:"schema"`
		}
		return jsonResult(fallbackResponse{
			Source: "static_fallback",
			Note:   "live schema unavailable — returning embedded schema.sql snapshot",
			Error:  poolErr.Error(),
			Schema: schemaSQL,
		})
	})

	s.AddTool(mcp.NewTool("get_daily_stats",
		mcp.WithDescription("Daily NEW_REQUEST/UPDATE_REQUEST/DEACTIVATE_REQUEST counts by system. "+
			"Veeva/ATOM5: from accounts_system_status by requested_date. "+
			"IWRS: from camp_ects_insert/update/deactivate by date_received, with is_uploaded_to_iwrs breakdown."),
		mcp.WithString("env", mcp.Required(), mcp.Description("Environment: dev, qa, or prod")),
		mcp.WithString("date", mcp.Description("Date in YYYY-MM-DD format. Defaults to today (CURRENT_DATE) if omitted.")),
	), func(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		args := req.GetArguments()
		p, err := pool.Get(ctx, argStr(args, "env"))
		if err != nil {
			return errResult(err)
		}
		result, err := db.GetDailyStats(ctx, p, argStr(args, "date"))
		if err != nil {
			return errResult(err)
		}
		return jsonResult(result)
	})
}

func jsonResult(v any) (*mcp.CallToolResult, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return errResult(err)
	}
	return mcp.NewToolResultText(string(b)), nil
}

func errResult(err error) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultError(err.Error()), nil
}

func argStr(args map[string]any, key string) string {
	if args == nil {
		return ""
	}
	s, _ := args[key].(string)
	return s
}

func splitCSV(v any) []string {
	s, _ := v.(string)
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}
