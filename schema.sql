-- Add new schema named "accma"
CREATE SCHEMA "accma";
-- Create "camp_veeva_timezones" table
CREATE TABLE "accma"."camp_veeva_timezones" (
  "timezone_description" character varying(255) NULL,
  "name" character varying(255) NULL,
  "timezone_id" character varying(255) NULL,
  "country_name" character varying(255) NULL,
  "country_code" character varying(2) NULL,
  "is_default" smallint NULL
);
-- Create index "veeva_country_code" to table: "camp_veeva_timezones"
CREATE INDEX "veeva_country_code" ON "accma"."camp_veeva_timezones" ("country_code");
-- Create index "veeva_country_name" to table: "camp_veeva_timezones"
CREATE INDEX "veeva_country_name" ON "accma"."camp_veeva_timezones" ("country_name");
-- Create index "veeva_timezone_id" to table: "camp_veeva_timezones"
CREATE INDEX "veeva_timezone_id" ON "accma"."camp_veeva_timezones" ("timezone_id");
-- Create "ects_info" table
CREATE TABLE "accma"."ects_info" (
  "study_alias" character varying(255) NULL,
  "site_reference_number" character varying(255) NULL,
  "first_name" character varying(255) NULL,
  "last_name" character varying(255) NULL,
  "email_address" character varying(255) NULL,
  "study_role" character varying(255) NULL,
  "iwrs_profile" character varying(255) NULL,
  "study_site_level_account_status" character varying(255) NULL,
  "phone" character varying(255) NULL,
  "last_login" timestamp NULL,
  "account_disable_date" timestamp NULL,
  "account_activation_date" timestamp NULL,
  CONSTRAINT "uk_ects_info_unique" UNIQUE ("first_name", "last_name", "email_address", "study_alias", "site_reference_number", "study_role", "iwrs_profile", "study_site_level_account_status", "phone")
);
-- Create index "idx_ects_info_lookup" to table: "ects_info"
CREATE INDEX "idx_ects_info_lookup" ON "accma"."ects_info" ("study_alias", "site_reference_number", (lower((email_address)::text)), (lower((first_name)::text)), (lower((last_name)::text)), "study_role");
-- Create "account_error_assignee" table
CREATE TABLE "accma"."account_error_assignee" (
  "error_assignee_id" bigserial NOT NULL,
  "account_id" bigint NOT NULL,
  "business_user_id" integer NOT NULL,
  PRIMARY KEY ("error_assignee_id")
);
-- Create "account_request_batch_run_status" table
CREATE TABLE "accma"."account_request_batch_run_status" (
  "id" integer NOT NULL DEFAULT nextval('accma.account_request_batch_run_status_id_seq1'::regclass),
  "batch_id" text NOT NULL,
  "total_request" text NOT NULL,
  "total_success" text NULL,
  "total_failed" text NULL,
  "run_date" timestamp NOT NULL,
  "is_completed" integer NULL DEFAULT 0,
  "system_id" integer NULL,
  PRIMARY KEY ("id")
);
-- Create "account_sites" table
CREATE TABLE "accma"."account_sites" (
  "account_id" bigint NOT NULL,
  "site_reference_number" text NULL,
  "postal_code" text NULL,
  "site_role" text NULL,
  "site_status" text NULL,
  "site_name" text NULL,
  "account_site_status_id" bigint NULL,
  "id" bigserial NOT NULL,
  "assignment_start_date" timestamp NULL,
  "assignment_end_date" timestamp NULL,
  "email_address" text NULL,
  "lilly_email_address" text NULL,
  "first_name" text NULL,
  "last_name" text NULL,
  "country_code" text NULL,
  "is_active_top_role" boolean NULL DEFAULT false,
  "country_name" text NULL,
  "is_milestones_passed" boolean NULL,
  "network_type" text NULL,
  "is_active" boolean NULL,
  "phone_number" character varying(240) NULL,
  "source_id" character varying(240) NULL,
  CONSTRAINT "account_sites_pk" PRIMARY KEY ("id"),
  CONSTRAINT "unique_account_site_status" UNIQUE ("account_site_status_id")
);
-- Create index "id" to table: "account_sites"
CREATE UNIQUE INDEX "id" ON "accma"."account_sites" ("id");
-- Create index "idx_account_sites_account_id" to table: "account_sites"
CREATE INDEX "idx_account_sites_account_id" ON "accma"."account_sites" ("account_id");
-- Create index "idx_account_sites_email_coalesce" to table: "account_sites"
CREATE INDEX "idx_account_sites_email_coalesce" ON "accma"."account_sites" ((lower(COALESCE(lilly_email_address, email_address))));
-- Create index "idx_account_sites_name" to table: "account_sites"
CREATE INDEX "idx_account_sites_name" ON "accma"."account_sites" ("account_id", "first_name", "last_name");
-- Create "account_sites_bkp_2026_01_30" table
CREATE TABLE "accma"."account_sites_bkp_2026_01_30" (
  "account_id" bigint NULL,
  "site_reference_number" text NULL,
  "postal_code" text NULL,
  "site_role" text NULL,
  "site_status" text NULL,
  "site_name" text NULL,
  "account_site_status_id" bigint NULL,
  "id" bigint NULL,
  "assignment_start_date" timestamp NULL,
  "assignment_end_date" timestamp NULL,
  "email_address" text NULL,
  "lilly_email_address" text NULL,
  "first_name" text NULL,
  "last_name" text NULL,
  "country_code" text NULL,
  "is_active_top_role" boolean NULL,
  "country_name" text NULL,
  "is_milestones_passed" boolean NULL,
  "network_type" text NULL,
  "is_active" boolean NULL,
  "phone_number" character varying(240) NULL,
  "source_id" character varying(240) NULL
);
-- Create "account_sites_bkp_2026_02_23" table
CREATE TABLE "accma"."account_sites_bkp_2026_02_23" (
  "account_id" bigint NULL,
  "site_reference_number" text NULL,
  "postal_code" text NULL,
  "site_role" text NULL,
  "site_status" text NULL,
  "site_name" text NULL,
  "account_site_status_id" bigint NULL,
  "id" bigint NULL,
  "assignment_start_date" timestamp NULL,
  "assignment_end_date" timestamp NULL,
  "email_address" text NULL,
  "lilly_email_address" text NULL,
  "first_name" text NULL,
  "last_name" text NULL,
  "country_code" text NULL,
  "is_active_top_role" boolean NULL,
  "country_name" text NULL,
  "is_milestones_passed" boolean NULL,
  "network_type" text NULL,
  "is_active" boolean NULL,
  "phone_number" character varying(240) NULL,
  "source_id" character varying(240) NULL
);
-- Create "account_system_activity_log" table
CREATE TABLE "accma"."account_system_activity_log" (
  "account_activity_log_id" bigserial NOT NULL,
  "activity_desc" text NOT NULL,
  "created_date" timestamp NOT NULL,
  "account_id" integer NOT NULL,
  "system_id" integer NOT NULL,
  CONSTRAINT "account_system_error_log_pkey" PRIMARY KEY ("account_activity_log_id")
);
-- Create "camp_user_saved_filters" table
CREATE TABLE "accma"."camp_user_saved_filters" (
  "filter_id" serial NOT NULL,
  "user_id" character varying(255) NOT NULL,
  "user_email_address" character varying(255) NULL,
  "filter_name" character varying(255) NOT NULL,
  "filter_criteria" jsonb NOT NULL,
  "is_default" boolean NULL DEFAULT false,
  "created_date" timestamp NULL DEFAULT CURRENT_TIMESTAMP,
  "created_by" character varying(255) NULL,
  "updated_date" timestamp NULL DEFAULT CURRENT_TIMESTAMP,
  "updated_by" character varying(255) NULL,
  PRIMARY KEY ("filter_id")
);
-- Create "accounts_system_errors_bkp_2026_01_30" table
CREATE TABLE "accma"."accounts_system_errors_bkp_2026_01_30" (
  "account_error_id" bigint NULL,
  "account_id" bigint NULL,
  "error_type_id" integer NULL,
  "system_id" integer NULL,
  "error_status_id" integer NULL,
  "reported_date" date NULL,
  "resolved_date" date NULL,
  "is_internally_fixed" boolean NULL,
  "study_id" integer NULL,
  "account_sites_id" bigint NULL
);
-- Create "accounts_system_errors_bkp_2026_02_23" table
CREATE TABLE "accma"."accounts_system_errors_bkp_2026_02_23" (
  "account_error_id" bigint NULL,
  "account_id" bigint NULL,
  "error_type_id" integer NULL,
  "system_id" integer NULL,
  "error_status_id" integer NULL,
  "reported_date" date NULL,
  "resolved_date" date NULL,
  "is_internally_fixed" boolean NULL,
  "study_id" integer NULL,
  "account_sites_id" bigint NULL
);
-- Create "accounts_system_status_bkp_2026_01_30" table
CREATE TABLE "accma"."accounts_system_status_bkp_2026_01_30" (
  "account_system_status_id" bigint NULL,
  "status_type_id" integer NULL,
  "requested_date" timestamp NULL,
  "completed_date" timestamp NULL,
  "deactivated_date" timestamp NULL,
  "study_id" integer NULL,
  "system_id" integer NULL,
  "system_role_id" integer NULL,
  "account_sites_id" bigint NULL,
  "account_id" integer NULL,
  "site_reference_number" text NULL,
  "site_personnel_role_id" integer NULL,
  "assignment_start_date" timestamp NULL,
  "training_status_type_id" integer NULL
);
-- Create "accounts_system_status_bkp_2026_02_23" table
CREATE TABLE "accma"."accounts_system_status_bkp_2026_02_23" (
  "account_system_status_id" bigint NULL,
  "status_type_id" integer NULL,
  "requested_date" timestamp NULL,
  "completed_date" timestamp NULL,
  "deactivated_date" timestamp NULL,
  "study_id" integer NULL,
  "system_id" integer NULL,
  "system_role_id" integer NULL,
  "account_sites_id" bigint NULL,
  "account_id" integer NULL,
  "site_reference_number" text NULL,
  "site_personnel_role_id" integer NULL,
  "assignment_start_date" timestamp NULL,
  "training_status_type_id" integer NULL
);
-- Create "atom5_active_study_tracker" table
CREATE TABLE "accma"."atom5_active_study_tracker" (
  "id" bigserial NOT NULL,
  "study_alias" text NOT NULL,
  "created_date" timestamp NOT NULL,
  PRIMARY KEY ("id")
);
-- Create index "idx_atom5_active_study_tracker_date" to table: "atom5_active_study_tracker"
CREATE INDEX "idx_atom5_active_study_tracker_date" ON "accma"."atom5_active_study_tracker" ("created_date");
-- Create index "idx_atom5_active_study_tracker_study_alias" to table: "atom5_active_study_tracker"
CREATE INDEX "idx_atom5_active_study_tracker_study_alias" ON "accma"."atom5_active_study_tracker" ("study_alias");
-- Create "veeva_user_training_report_data_table" table
CREATE TABLE "accma"."veeva_user_training_report_data_table" (
  "last_name" text NULL,
  "first_name" text NULL,
  "email" text NULL,
  "company" text NULL,
  "learning_system_username" text NULL,
  "vault_username" text NULL,
  "cdms_user_status" text NULL,
  "study" text NULL,
  "study_role" text NULL,
  "study_access" text NULL,
  "site_access" text NULL,
  "country_access" text NULL,
  "training_status_for_cdms_access" text NULL,
  "training_required_for_cdms_access" text NULL,
  "date_assigned_to_study" timestamptz NULL,
  "last_modified_date" timestamptz NULL,
  "curriculums_assigned" text NULL,
  "training_completion_date" timestamptz NULL,
  "veeva_training_report_id" bigserial NOT NULL,
  "user_type" text NULL,
  PRIMARY KEY ("veeva_training_report_id")
);
-- Create index "idx_veeva_training_status" to table: "veeva_user_training_report_data_table"
CREATE INDEX "idx_veeva_training_status" ON "accma"."veeva_user_training_report_data_table" ("training_status_for_cdms_access");
-- Create index "idx_vutrd_email_study_lastmod" to table: "veeva_user_training_report_data_table"
CREATE INDEX "idx_vutrd_email_study_lastmod" ON "accma"."veeva_user_training_report_data_table" ((lower(email)), "study", "last_modified_date" DESC);
-- Create "veeva_site_mapping" table
CREATE TABLE "accma"."veeva_site_mapping" (
  "veeva_site_id" serial NOT NULL,
  "veeva_training_report_id" bigint NOT NULL,
  "site_reference_number" text NULL,
  PRIMARY KEY ("veeva_site_id")
);
-- Create "veeva_job_runs" table
CREATE TABLE "accma"."veeva_job_runs" (
  "id" serial NOT NULL,
  "last_run_time" timestamp NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY ("id")
);
-- Create index "idx_job_id_last_run_time" to table: "veeva_job_runs"
CREATE INDEX "idx_job_id_last_run_time" ON "accma"."veeva_job_runs" ("last_run_time" DESC);
-- Create "atom5_share_point_site_account_data" table
CREATE TABLE "accma"."atom5_share_point_site_account_data" (
  "id" bigserial NOT NULL,
  "site_code" text NULL,
  "site_name" text NULL,
  "alert_email" text NULL,
  "country" text NULL,
  "change_comments" text NULL,
  "date_requested" timestamp NULL,
  "date_completed" timestamp NULL,
  "requester" text NULL DEFAULT 'CAMP',
  "sharepoint_sent_date" timestamp NULL,
  "trial" text NULL,
  "timezone" text NULL,
  PRIMARY KEY ("id")
);
-- Create "atom5_share_point_user_account_data" table
CREATE TABLE "accma"."atom5_share_point_user_account_data" (
  "id" bigserial NOT NULL,
  "email_address" text NULL,
  "first_name" text NULL,
  "last_name" text NULL,
  "study_role" text NULL,
  "site_access" text NULL,
  "training_account_required" text NULL,
  "change_comments" text NULL,
  "study_alias" text NULL,
  "date_requested" timestamp NULL,
  "date_completed" timestamp NULL,
  "requester" text NULL DEFAULT 'CAMP',
  "sharepoint_sent_date" timestamp NULL,
  PRIMARY KEY ("id")
);
-- Create "atom5_site_accounts" table
CREATE TABLE "accma"."atom5_site_accounts" (
  "id" bigserial NOT NULL,
  "study_alias" text NOT NULL,
  "site_code" text NOT NULL,
  "site_name" text NOT NULL,
  "alert_email" text NOT NULL,
  "country" text NOT NULL,
  "change_comments" text NOT NULL,
  "request_type" text NOT NULL,
  "is_added_to_excel" boolean NOT NULL,
  "last_modified" timestamp NOT NULL,
  "timezone" text NULL,
  PRIMARY KEY ("id")
);
-- Create index "idx_atom5_site_accounts_date" to table: "atom5_site_accounts"
CREATE INDEX "idx_atom5_site_accounts_date" ON "accma"."atom5_site_accounts" ("last_modified");
-- Create index "idx_atom5_site_accounts_site_code" to table: "atom5_site_accounts"
CREATE INDEX "idx_atom5_site_accounts_site_code" ON "accma"."atom5_site_accounts" ("site_code");
-- Create index "idx_atom5_site_accounts_study_alias" to table: "atom5_site_accounts"
CREATE INDEX "idx_atom5_site_accounts_study_alias" ON "accma"."atom5_site_accounts" ("study_alias");
-- Create "atom5_site_mapping" table
CREATE TABLE "accma"."atom5_site_mapping" (
  "atom5_site_id" serial NOT NULL,
  "atom5_training_report_id" bigint NOT NULL,
  "site_reference_number" text NULL
);
-- Create "atom5_staff_details_report" table
CREATE TABLE "accma"."atom5_staff_details_report" (
  "atom5_training_report_id" serial NOT NULL,
  "trial_alias" text NOT NULL,
  "lilly_global_id" text NULL,
  "first_name" text NULL,
  "last_name" text NULL,
  "email_address" text NULL,
  "is_super_admin" text NULL,
  "drug_manager" text NULL,
  "staff_creation_date" text NULL,
  "suspended_date" text NULL,
  "trial_status" text NULL,
  "camp_account_status" text NULL,
  "site_reference_number" text NULL,
  "site_name" text NULL,
  "site_role" text NULL,
  "training_status" text NULL,
  PRIMARY KEY ("atom5_training_report_id")
);
-- Create index "idx_email_trial_site" to table: "atom5_staff_details_report"
CREATE INDEX "idx_email_trial_site" ON "accma"."atom5_staff_details_report" ((lower(email_address)), "trial_alias", "site_reference_number");
-- Create "business_admin_users" table
CREATE TABLE "accma"."business_admin_users" (
  "business_user_id" smallserial NOT NULL,
  "first_name" text NOT NULL,
  "last_name" text NULL,
  "email_address" text NOT NULL,
  "is_active" boolean NOT NULL,
  "created_date" timestamp NOT NULL,
  "created_by" text NOT NULL,
  "updated_date" timestamp NOT NULL,
  "updated_by" text NOT NULL,
  PRIMARY KEY ("business_user_id")
);
-- Create "camp_audit_log" table
CREATE TABLE "accma"."camp_audit_log" (
  "id" bigserial NOT NULL,
  "log_type" character varying(20) NULL,
  "entity_name" text NULL,
  "entity_primary_key" text NULL,
  "entity_pk_value" text NULL,
  "entity_foreign_key" text NULL,
  "entity_fk_value" text NULL,
  "field_name" text NULL,
  "operation" text NULL,
  "old_value" text NULL,
  "new_value" text NULL,
  "record" jsonb NULL,
  "user_name" text NULL,
  "comment" text NULL,
  "change_time" timestamp NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY ("id"),
  CONSTRAINT "camp_audit_log_log_type_check" CHECK ((log_type)::text = ANY ((ARRAY['account'::character varying, 'site'::character varying])::text[])),
  CONSTRAINT "camp_audit_log_operation_check" CHECK (operation = ANY (ARRAY['INSERT'::text, 'UPDATE'::text, 'DELETE'::text]))
);
-- Create "camp_db_manual_migrations" table
CREATE TABLE "accma"."camp_db_manual_migrations" (
  "name" text NULL,
  "migration_date" timestamp NULL DEFAULT CURRENT_TIMESTAMP
);
-- Create "camp_default_postal_code" table
CREATE TABLE "accma"."camp_default_postal_code" (
  "country_cd" character varying(2) NULL,
  "geo_country_id" bigint NULL,
  "country_nm" character varying(100) NULL,
  "country_long_nm" character varying(100) NULL,
  "postal_cd" character varying(20) NULL,
  "geo_glbl_postal_id" bigint NULL,
  "region_1_iso2_cd" character varying(100) NULL,
  "city_nm" character varying(100) NULL,
  "region_1_nm" character varying(100) NULL,
  "region_2_nm" character varying(100) NULL,
  "geo_postal_timezone_id" bigint NULL,
  "time_zone_olson_desc" character varying(100) NULL,
  "time_zone_utc_desc" character varying(20) NULL,
  "time_zone_dst_desc" character varying(20) NULL,
  "lgcl_dlt_flg" smallint NULL
);
-- Create index "default_country_cd" to table: "camp_default_postal_code"
CREATE INDEX "default_country_cd" ON "accma"."camp_default_postal_code" ("country_cd");
-- Create index "default_postal_cd" to table: "camp_default_postal_code"
CREATE INDEX "default_postal_cd" ON "accma"."camp_default_postal_code" ("postal_cd");
-- Create index "default_postal_country" to table: "camp_default_postal_code"
CREATE INDEX "default_postal_country" ON "accma"."camp_default_postal_code" ("postal_cd", "country_cd");
-- Create "camp_ects_deactivate" table
CREATE TABLE "accma"."camp_ects_deactivate" (
  "source_id" character varying(100) NOT NULL,
  "eph_id" character varying(100) NOT NULL,
  "trial_alias" character varying(11) NOT NULL,
  "siteid" character varying(10) NOT NULL,
  "first_name" character varying(255) NOT NULL,
  "last_name" character varying(255) NOT NULL,
  "email_address" character varying(255) NOT NULL,
  "study_role" character varying(50) NOT NULL,
  "iwrs_profile" character varying(20) NOT NULL,
  "phone_number" character varying(30) NULL,
  "date_received" date NOT NULL,
  "status" character varying(5) NULL,
  "is_uploaded_to_iwrs" boolean NULL DEFAULT false
);
-- Create "camp_ects_insert" table
CREATE TABLE "accma"."camp_ects_insert" (
  "source_id" character varying(100) NOT NULL,
  "eph_id" character varying(100) NOT NULL,
  "trial_alias" character varying(11) NOT NULL,
  "siteid" character varying(10) NOT NULL,
  "first_name" character varying(255) NOT NULL,
  "last_name" character varying(255) NOT NULL,
  "email_address" character varying(255) NOT NULL,
  "study_role" character varying(50) NOT NULL,
  "iwrs_profile" character varying(20) NOT NULL,
  "phone_number" character varying(30) NULL,
  "date_received" date NOT NULL,
  "status" character varying(5) NULL,
  "is_uploaded_to_iwrs" boolean NULL DEFAULT false
);
-- Create "camp_ects_update" table
CREATE TABLE "accma"."camp_ects_update" (
  "source_id" character varying(100) NOT NULL,
  "eph_id" character varying(100) NOT NULL,
  "trial_alias" character varying(11) NOT NULL,
  "siteid" character varying(10) NOT NULL,
  "first_name" character varying(255) NOT NULL,
  "last_name" character varying(255) NOT NULL,
  "email_address" character varying(255) NOT NULL,
  "study_role" character varying(50) NOT NULL,
  "iwrs_profile" character varying(20) NOT NULL,
  "phone_number" character varying(30) NULL,
  "date_received" date NOT NULL,
  "updated_columns" character varying(4000) NOT NULL,
  "old_values" character varying(4000) NOT NULL,
  "status" character varying(5) NULL,
  "is_uploaded_to_iwrs" boolean NULL DEFAULT false
);
-- Create "camp_entity_types" table
CREATE TABLE "accma"."camp_entity_types" (
  "entity_type_id" smallserial NOT NULL,
  "entity_type" text NOT NULL,
  "created_date" timestamp NOT NULL,
  "created_by" text NOT NULL,
  "updated_date" timestamp NOT NULL,
  "updated_by" text NOT NULL,
  PRIMARY KEY ("entity_type_id", "entity_type")
);
-- Create "camp_failed_records" table
CREATE TABLE "accma"."camp_failed_records" (
  "id" bigserial NOT NULL,
  "key" text NULL,
  "error_message" text NULL,
  "created_at" timestamp NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY ("id")
);
-- Create "camp_geo_sad_replica" table
CREATE TABLE "accma"."camp_geo_sad_replica" (
  "id" text NULL,
  "country_cd" character varying(2) NULL,
  "geo_country_id" bigint NULL,
  "country_nm" character varying(100) NULL,
  "country_long_nm" character varying(100) NULL,
  "postal_cd" character varying(20) NULL,
  "geo_glbl_postal_id" bigint NULL,
  "region_1_iso2_cd" character varying(100) NULL,
  "city_nm" character varying(100) NULL,
  "region_1_nm" character varying(100) NULL,
  "region_2_nm" character varying(100) NULL,
  "geo_postal_timezone_id" bigint NULL,
  "time_zone_olson_desc" character varying(100) NULL,
  "time_zone_utc_desc" character varying(20) NULL,
  "time_zone_dst_desc" character varying(20) NULL,
  "lgcl_dlt_flg" smallint NULL
);
-- Create index "idx_country_cd" to table: "camp_geo_sad_replica"
CREATE INDEX "idx_country_cd" ON "accma"."camp_geo_sad_replica" ("country_cd");
-- Create index "idx_id" to table: "camp_geo_sad_replica"
CREATE INDEX "idx_id" ON "accma"."camp_geo_sad_replica" ("id");
-- Create index "idx_postal_cd" to table: "camp_geo_sad_replica"
CREATE INDEX "idx_postal_cd" ON "accma"."camp_geo_sad_replica" ("postal_cd");
-- Create index "idx_postal_country" to table: "camp_geo_sad_replica"
CREATE INDEX "idx_postal_country" ON "accma"."camp_geo_sad_replica" ("postal_cd", "country_cd");
-- Create "camp_ingestion_roles" table
CREATE TABLE "accma"."camp_ingestion_roles" (
  "camp_ingestion_role_id" serial NOT NULL,
  "site_personnel_role_id" integer NOT NULL,
  "system_id" integer NOT NULL,
  "created_date" timestamp NOT NULL,
  "created_by" text NOT NULL,
  "updated_date" timestamp NOT NULL,
  "updated_by" text NOT NULL,
  "role_hierarchy" integer NULL,
  "is_sponsor" boolean NULL DEFAULT false,
  "is_valid_for_all_sites" boolean NULL DEFAULT false,
  PRIMARY KEY ("camp_ingestion_role_id")
);
-- Create "account_training_status_types" table
CREATE TABLE "accma"."account_training_status_types" (
  "training_status_type_id" integer NOT NULL,
  "status" text NOT NULL,
  "description" text NOT NULL,
  "show_in_ui" boolean NOT NULL DEFAULT true,
  "display_name" text NULL,
  "ui_color_code" text NULL
);
-- Add new schema named "public"
CREATE SCHEMA IF NOT EXISTS "public";
-- Set comment to schema: "public"
COMMENT ON SCHEMA "public" IS 'standard public schema';
-- Create "camp_db_migrations" table
CREATE TABLE "accma"."camp_db_migrations" (
  "name" text NULL,
  "migration_date" timestamp NULL DEFAULT CURRENT_TIMESTAMP,
  "id" bigserial NOT NULL,
  PRIMARY KEY ("id")
);
-- Create "camp_users_account_daily_delta" table
CREATE TABLE "accma"."camp_users_account_daily_delta" (
  "eph_personnel_id" text NOT NULL,
  "change_details" jsonb NOT NULL,
  "site_role" text NOT NULL,
  "timestamp" timestamp NOT NULL DEFAULT now(),
  "study_alias" text NOT NULL,
  "operation" text NULL,
  "site_reference_number" text NULL,
  "source_key" text NULL,
  "is_existing_ephid" boolean NULL,
  "assignment_start_date" timestamp NULL
);
-- Create "eph_personnel_id_update_logs" table
CREATE TABLE "accma"."eph_personnel_id_update_logs" (
  "eph_update_log_id" bigserial NOT NULL,
  "original_eph_personnel_id" text NULL,
  "current_eph_personnel_id" text NULL,
  "updated_date" timestamp NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY ("eph_update_log_id")
);
-- Create "error_notification_status" table
CREATE TABLE "accma"."error_notification_status" (
  "notification_status_id" bigserial NOT NULL,
  "reminder_type_id" integer NOT NULL,
  "notified_date" timestamp NOT NULL,
  "account_id" bigint NOT NULL,
  PRIMARY KEY ("notification_status_id")
);
-- Create "error_reminder_types" table
CREATE TABLE "accma"."error_reminder_types" (
  "reminder_type_id" smallserial NOT NULL,
  "reminder_type" text NOT NULL,
  "system_id" integer NOT NULL,
  "reminder_status" text NOT NULL,
  PRIMARY KEY ("reminder_type_id")
);
-- Create "exception_audit_log" table
CREATE TABLE "accma"."exception_audit_log" (
  "id" serial NOT NULL,
  "step_function_name" text NULL,
  "step_name" text NULL,
  "function_name" text NULL,
  "error_desc" text NULL,
  "error_type" text NULL,
  "created_date" timestamp NOT NULL
);
-- Create "phone_number_source_id" table
CREATE TABLE "accma"."phone_number_source_id" (
  "id" text NOT NULL,
  "source_id" character varying(255) NULL,
  "phone_number" character varying(255) NULL
);
-- Create "site_personnel_roles" table
CREATE TABLE "accma"."site_personnel_roles" (
  "site_personnel_role_id" serial NOT NULL,
  "role_name" text NOT NULL,
  "role_priority" integer NOT NULL,
  "created_date" timestamp NOT NULL,
  "created_by" text NOT NULL,
  "updated_date" timestamp NOT NULL,
  "updated_by" text NOT NULL,
  "is_sponsor" boolean NULL DEFAULT false,
  PRIMARY KEY ("site_personnel_role_id")
);
-- Create index "idx_site_personnel_roles_site_personnel_role_id" to table: "site_personnel_roles"
CREATE INDEX "idx_site_personnel_roles_site_personnel_role_id" ON "accma"."site_personnel_roles" ("site_personnel_role_id");
-- Create "user_accounts_source_deleted" table
CREATE TABLE "accma"."user_accounts_source_deleted" (
  "id" text NOT NULL,
  "eph_personnel_id" text NULL,
  "study_alias" text NULL,
  "site_reference_number" text NULL,
  "first_name" text NULL,
  "last_name" text NULL,
  "full_name" text NULL,
  "email_address" text NULL,
  "site_role" text NULL,
  "deleted_on" timestamp NULL DEFAULT CURRENT_TIMESTAMP
);
-- Create "user_accounts_source" table
CREATE TABLE "accma"."user_accounts_source" (
  "id" text NOT NULL,
  "eph_personnel_id" text NULL,
  "study_alias" text NULL,
  "study_status" text NULL,
  "site_reference_number" text NULL,
  "site_name" text NULL,
  "site_status" text NULL,
  "postal_code" text NULL,
  "first_name" text NULL,
  "last_name" text NULL,
  "full_name" text NULL,
  "country_code" text NULL,
  "email_address" text NULL,
  "site_role" text NULL,
  "assignment_start_date" timestamp NULL,
  "assignment_end_date" timestamp NULL,
  "imported_date" timestamp NULL,
  "updated_date" timestamp NULL,
  "country_name" text NULL,
  "is_milestones_passed" boolean NULL,
  "network_type" text NULL,
  "is_active" boolean NULL,
  "phone_number" character varying(240) NULL,
  "source_id" character varying(240) NULL,
  "is_sponsor" boolean NULL DEFAULT false
);
-- Create index "idx_uas_active_enddate" to table: "user_accounts_source"
CREATE INDEX "idx_uas_active_enddate" ON "accma"."user_accounts_source" ("is_active", "assignment_end_date");
-- Create index "idx_user_accounts_source_personnel_study_site_role" to table: "user_accounts_source"
CREATE INDEX "idx_user_accounts_source_personnel_study_site_role" ON "accma"."user_accounts_source" ("eph_personnel_id", "study_alias", "site_reference_number", "site_role");
-- Create "user_accounts_camp" table
CREATE TABLE "accma"."user_accounts_camp" (
  "eph_personnel_id" text NULL,
  "updated_date" timestamp NULL,
  "updated_by" text NULL,
  "account_id" bigserial NOT NULL,
  PRIMARY KEY ("account_id"),
  CONSTRAINT "unique_eph_personnel_id" UNIQUE ("eph_personnel_id")
);
-- Create index "idx_user_accounts_camp_account_id" to table: "user_accounts_camp"
CREATE INDEX "idx_user_accounts_camp_account_id" ON "accma"."user_accounts_camp" ("account_id");
-- Create "user_account_requests_daily_run" table
CREATE TABLE "accma"."user_account_requests_daily_run" (
  "daily_run_id" serial NOT NULL,
  "account_id" bigint NOT NULL,
  "batch_id" text NOT NULL,
  "study_id" integer NOT NULL,
  "system_id" integer NOT NULL,
  "scheduled_date_time" timestamp NOT NULL,
  "processed_date_time" timestamp NOT NULL,
  "change_comments" text NULL,
  "site_id" bigint NULL,
  "change_details" text NULL,
  PRIMARY KEY ("daily_run_id")
);
-- Create index "idx_uar_dr_account_site_study" to table: "user_account_requests_daily_run"
CREATE INDEX "idx_uar_dr_account_site_study" ON "accma"."user_account_requests_daily_run" ("account_id", "site_id", "study_id", "processed_date_time" DESC);
-- Create index "idx_user_account_requests_daily_run_site" to table: "user_account_requests_daily_run"
CREATE INDEX "idx_user_account_requests_daily_run_site" ON "accma"."user_account_requests_daily_run" ("site_id", "system_id", "study_id", "processed_date_time" DESC);
-- Create "study_site_data" table
CREATE TABLE "accma"."study_site_data" (
  "site_reference_number" text NOT NULL,
  "study_alias" text NOT NULL,
  "network_type" text NULL,
  "study_site_status" text NULL,
  "site_milestone_data" jsonb NULL,
  "last_updated_dt" timestamp NULL DEFAULT CURRENT_TIMESTAMP,
  "country_name" text NOT NULL,
  "country_code" text NOT NULL,
  "site_name" text NULL,
  "alert_email" text NULL,
  "site_timezone" text NULL,
  "timezone" text NULL,
  PRIMARY KEY ("site_reference_number", "study_alias", "country_code")
);
-- Create index "idx_study_site_data" to table: "study_site_data"
CREATE INDEX "idx_study_site_data" ON "accma"."study_site_data" ("site_reference_number");
-- Create index "idx_study_site_data_country_code_name" to table: "study_site_data"
CREATE INDEX "idx_study_site_data_country_code_name" ON "accma"."study_site_data" ("country_code", "country_name");
-- Create "study_system_role_config" table
CREATE TABLE "accma"."study_system_role_config" (
  "study_system_role_id" serial NOT NULL,
  "study_id" integer NOT NULL,
  "system_id" integer NOT NULL,
  "site_personnel_role_id" integer NOT NULL,
  "created_date" timestamp NOT NULL,
  "created_by" text NOT NULL,
  "updated_date" timestamp NOT NULL,
  "updated_by" text NOT NULL,
  PRIMARY KEY ("study_system_role_id")
);
-- Create "study_system_role_mapping" table
CREATE TABLE "accma"."study_system_role_mapping" (
  "id" serial NOT NULL,
  "study_id" integer NOT NULL,
  "system_id" integer NOT NULL,
  "site_personnel_role_id" integer NOT NULL,
  "system_role_id" integer NOT NULL,
  "is_special_role" integer NOT NULL,
  "created_date" timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
  "created_by" text NOT NULL DEFAULT 'system',
  "updated_date" timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
  "updated_by" text NOT NULL DEFAULT 'system',
  CONSTRAINT "study_system_role_mapping_pkey1" PRIMARY KEY ("id")
);
-- Create index "idx_study_system_role_mapping_study_id" to table: "study_system_role_mapping"
CREATE INDEX "idx_study_system_role_mapping_study_id" ON "accma"."study_system_role_mapping" ("study_id");
-- Create "system_audit_log" table
CREATE TABLE "accma"."system_audit_log" (
  "id" uuid NOT NULL DEFAULT public.uuid_generate_v4(),
  "user_id" text NOT NULL,
  "user_name" text NULL,
  "user_email" text NULL,
  "action" text NOT NULL,
  "action_date" date NOT NULL DEFAULT CURRENT_DATE,
  "action_time" timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
  "action_description" text NULL,
  "ip_address" text NULL,
  PRIMARY KEY ("id")
);
-- Create "system_configurations" table
CREATE TABLE "accma"."system_configurations" (
  "config_type" text NOT NULL,
  "message" text NOT NULL
);
-- Create "system_email_config" table
CREATE TABLE "accma"."system_email_config" (
  "system_email_config_id" serial NOT NULL,
  "system_id" integer NOT NULL,
  "email_address" text NOT NULL,
  "user_type" text NOT NULL,
  "is_active" integer NOT NULL,
  PRIMARY KEY ("system_email_config_id")
);
-- Create "system_error_status" table
CREATE TABLE "accma"."system_error_status" (
  "error_status_id" serial NOT NULL,
  "error_status" text NOT NULL,
  "system_id" integer NULL,
  PRIMARY KEY ("error_status_id")
);
-- Create index "idx_system_error_status_error_status_id" to table: "system_error_status"
CREATE INDEX "idx_system_error_status_error_status_id" ON "accma"."system_error_status" ("error_status_id");
-- Create "system_error_types" table
CREATE TABLE "accma"."system_error_types" (
  "error_type_id" serial NOT NULL,
  "error_type_code" text NOT NULL,
  "error_type_desc" text NOT NULL,
  "created_date" timestamp NOT NULL,
  "created_by" text NOT NULL,
  "updated_date" timestamp NULL,
  "updated_by" text NULL,
  "system_id" integer NOT NULL,
  "is_action_required" boolean NULL,
  "display_name" text NULL,
  PRIMARY KEY ("error_type_id")
);
-- Create "system_excluded_email_config" table
CREATE TABLE "accma"."system_excluded_email_config" (
  "excluded_email_config_id" smallserial NOT NULL,
  "system_id" integer NOT NULL,
  "email_address" text NOT NULL,
  "study_id" integer NULL
);
-- Create "system_ingestion_status" table
CREATE TABLE "accma"."system_ingestion_status" (
  "system_name" character varying(255) NULL,
  "system_id" integer NULL,
  "last_ingestion_timestamp" timestamp NULL
);
-- Create "system_processed_result" table
CREATE TABLE "accma"."system_processed_result" (
  "id" bigserial NOT NULL,
  "system_id" bigint NOT NULL,
  "study_id" bigint NOT NULL,
  "batch_id" bigint NOT NULL,
  "first_name" text NULL,
  "last_name" text NULL,
  "email_address" text NULL,
  "time_zone" text NULL,
  "study_role" text NULL,
  "site_name" text NULL,
  "requested_date" timestamp NULL,
  "approved_date" text NULL,
  "comments" text NULL,
  "status" text NULL,
  "site_access" text NULL,
  "sip_role" text NULL,
  "study_access" text NULL,
  "fed_id" text NULL,
  "postal_code" text NULL,
  "email_sent_date" timestamp NULL,
  "account_id" bigint NOT NULL,
  "include_in_reqest" integer NULL,
  "user_type" text NULL,
  "is_processed" boolean NULL DEFAULT false,
  PRIMARY KEY ("id")
);
-- Create "system_profile" table
CREATE TABLE "accma"."system_profile" (
  "id" serial NOT NULL,
  "system_id" integer NOT NULL,
  "system_profile" text NOT NULL,
  "created_date" timestamp NOT NULL,
  "created_by" text NOT NULL,
  "updated_date" timestamp NOT NULL,
  "updated_by" text NOT NULL,
  CONSTRAINT "system_profile_pkey1" PRIMARY KEY ("id")
);
-- Create "system_role_profile_mapping" table
CREATE TABLE "accma"."system_role_profile_mapping" (
  "id" serial NOT NULL,
  "system_id" integer NOT NULL,
  "site_personnel_role_id" integer NOT NULL,
  "system_role_id" integer NOT NULL,
  "created_date" timestamp NOT NULL,
  "created_by" text NOT NULL,
  "updated_date" timestamp NOT NULL,
  "updated_by" text NOT NULL,
  "system_profile_id" integer NOT NULL,
  CONSTRAINT "system_role_mapping_pkey1" PRIMARY KEY ("id")
);
-- Create "system_roles" table
CREATE TABLE "accma"."system_roles" (
  "id" serial NOT NULL,
  "system_id" integer NOT NULL,
  "system_role_name" text NOT NULL,
  "created_date" timestamp NOT NULL,
  "created_by" text NOT NULL,
  "updated_date" timestamp NOT NULL,
  "updated_by" text NOT NULL,
  "role_code" text NULL,
  PRIMARY KEY ("id")
);
-- Create index "idx_system_roles_id" to table: "system_roles"
CREATE INDEX "idx_system_roles_id" ON "accma"."system_roles" ("id");
-- Create "umg_email_data" table
CREATE TABLE "accma"."umg_email_data" (
  "id" bigserial NOT NULL,
  "user_name" text NULL,
  "email_address" text NULL,
  "user_title" text NULL,
  "first_name" text NULL,
  "last_name" text NULL,
  "company" text NULL,
  "fed_id" text NULL,
  "language" text NULL DEFAULT 'en_US',
  "locale" text NULL DEFAULT 'en_US',
  "time_zone" text NULL,
  "security_policy" text NULL,
  "cross_study_role" text NULL,
  "activation_date" timestamp NULL,
  "send_welcome_email" character(3) NULL DEFAULT 'Yes',
  "add_as_principal_investigator" character(3) NULL DEFAULT 'No',
  "study_alias" text NULL,
  "study_environment" text NULL,
  "access_to_all_environments" character(3) NULL DEFAULT 'No',
  "study_role" text NULL,
  "access_to_all_sites" character(3) NULL DEFAULT 'No',
  "study_access" text NULL,
  "country_access" text NULL,
  "site_access" text NULL,
  "ignore_lms_status" character(3) NULL DEFAULT 'No',
  "vault_access" text NULL,
  "date_requested" timestamp NULL,
  "date_completed" timestamp NULL,
  "zendesk_ticket_number" bigint NULL,
  "comments" text NULL,
  "requester" text NULL DEFAULT 'CAMP',
  "email_sent_date" timestamp NULL,
  "user_type" text NULL,
  PRIMARY KEY ("id"),
  CONSTRAINT "umg_email_data_access_to_all_environments_check" CHECK (access_to_all_environments = ANY (ARRAY['Yes'::bpchar, 'No'::bpchar])),
  CONSTRAINT "umg_email_data_access_to_all_sites_check" CHECK (access_to_all_sites = ANY (ARRAY['Yes'::bpchar, 'No'::bpchar])),
  CONSTRAINT "umg_email_data_add_as_principal_investigator_check" CHECK (add_as_principal_investigator = ANY (ARRAY['Yes'::bpchar, 'No'::bpchar])),
  CONSTRAINT "umg_email_data_ignore_lms_status_check" CHECK (ignore_lms_status = ANY (ARRAY['Yes'::bpchar, 'No'::bpchar])),
  CONSTRAINT "umg_email_data_send_welcome_email_check" CHECK (send_welcome_email = ANY (ARRAY['Yes'::bpchar, 'No'::bpchar]))
);
-- Create "systems_cache" table
CREATE TABLE "accma"."systems_cache" (
  "id" character varying(500) NOT NULL,
  "api_response" jsonb NOT NULL,
  "fetched_at" timestamptz NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY ("id")
);
-- Create "accounts_system_errors" table
CREATE TABLE "accma"."accounts_system_errors" (
  "account_error_id" bigserial NOT NULL,
  "account_id" bigint NOT NULL,
  "error_type_id" integer NOT NULL,
  "system_id" integer NOT NULL,
  "error_status_id" integer NOT NULL,
  "reported_date" date NOT NULL,
  "resolved_date" date NULL,
  "is_internally_fixed" boolean NULL,
  "study_id" integer NOT NULL,
  "account_sites_id" bigint NOT NULL,
  PRIMARY KEY ("account_error_id"),
  CONSTRAINT "unique_combination_updated" UNIQUE ("account_id", "error_type_id", "system_id", "study_id", "account_sites_id"),
  CONSTRAINT "accounts_system_errors_account_sites_id_fk" FOREIGN KEY ("account_sites_id") REFERENCES "accma"."account_sites" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION
);
-- Create index "idx_accounts_system_errors_account_error_type_status" to table: "accounts_system_errors"
CREATE INDEX "idx_accounts_system_errors_account_error_type_status" ON "accma"."accounts_system_errors" ("account_sites_id", "error_type_id", "error_status_id");
-- Create index "idx_accounts_system_errors_account_id" to table: "accounts_system_errors"
CREATE INDEX "idx_accounts_system_errors_account_id" ON "accma"."accounts_system_errors" ("account_id");
-- Create "accounts_system_status" table
CREATE TABLE "accma"."accounts_system_status" (
  "account_system_status_id" bigserial NOT NULL,
  "status_type_id" integer NULL,
  "requested_date" timestamp NULL,
  "completed_date" timestamp NULL,
  "deactivated_date" timestamp NULL,
  "study_id" integer NOT NULL,
  "system_id" integer NOT NULL,
  "system_role_id" integer NULL,
  "account_sites_id" bigint NULL,
  "account_id" integer NULL,
  "site_reference_number" text NULL,
  "site_personnel_role_id" integer NULL,
  "assignment_start_date" timestamp NULL,
  "training_status_type_id" integer NULL DEFAULT 6,
  PRIMARY KEY ("account_system_status_id"),
  CONSTRAINT "accounts_system_status_account_sites_fk" FOREIGN KEY ("account_sites_id") REFERENCES "accma"."account_sites" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION
);
-- Create index "idx_accounts_system_status_account_id" to table: "accounts_system_status"
CREATE INDEX "idx_accounts_system_status_account_id" ON "accma"."accounts_system_status" ("account_id");
-- Create index "idx_accounts_system_status_account_system_status_id" to table: "accounts_system_status"
CREATE INDEX "idx_accounts_system_status_account_system_status_id" ON "accma"."accounts_system_status" ("account_system_status_id");
-- Create index "idx_accounts_system_status_dates" to table: "accounts_system_status"
CREATE INDEX "idx_accounts_system_status_dates" ON "accma"."accounts_system_status" ("requested_date", "completed_date", "deactivated_date");
-- Create index "idx_accounts_system_status_status_system_study_accounts" to table: "accounts_system_status"
CREATE INDEX "idx_accounts_system_status_status_system_study_accounts" ON "accma"."accounts_system_status" ("status_type_id", "system_id", "study_id", "account_sites_id", "account_id");
-- Create index "idx_accounts_system_status_study_id" to table: "accounts_system_status"
CREATE INDEX "idx_accounts_system_status_study_id" ON "accma"."accounts_system_status" ("study_id");
-- Create index "idx_status_status_training" to table: "accounts_system_status"
CREATE INDEX "idx_status_status_training" ON "accma"."accounts_system_status" ("training_status_type_id", "status_type_id", "system_id", "study_id");
-- Create index "idx_status_sys_id_site_id" to table: "accounts_system_status"
CREATE INDEX "idx_status_sys_id_site_id" ON "accma"."accounts_system_status" ("system_id", "account_sites_id");
-- Create "account_status_types" table
CREATE TABLE "accma"."account_status_types" (
  "status_type_id" serial NOT NULL,
  "status" text NOT NULL,
  "description" text NOT NULL,
  "show_in_ui" boolean NOT NULL DEFAULT true,
  "display_name" text NULL,
  "ui_color_code" text NULL,
  PRIMARY KEY ("status_type_id")
);
-- Create "accounts_system_status_logs" table
CREATE TABLE "accma"."accounts_system_status_logs" (
  "id" bigserial NOT NULL,
  "accounts_system_status_id" bigint NOT NULL,
  "account_status_type_id" integer NOT NULL,
  "status_date" timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP,
  "account_training_status_type_id" integer NULL,
  CONSTRAINT "accounts_system_status_logs_pk" PRIMARY KEY ("id"),
  CONSTRAINT "accounts_system_status_logs_account_status_types_fk" FOREIGN KEY ("account_status_type_id") REFERENCES "accma"."account_status_types" ("status_type_id") ON UPDATE NO ACTION ON DELETE NO ACTION
);
-- Create index "acc_status_logs_acc_status_id_idx" to table: "accounts_system_status_logs"
CREATE INDEX "acc_status_logs_acc_status_id_idx" ON "accma"."accounts_system_status_logs" ("accounts_system_status_id", "account_status_type_id");
-- Create "study" table
CREATE TABLE "accma"."study" (
  "id" serial NOT NULL,
  "study_number" text NOT NULL,
  "study_alias" text NULL,
  "study_desc" text NULL,
  "study_phase" text NULL,
  "study_status" text NULL,
  "created_date" timestamp NOT NULL,
  "created_by" text NOT NULL,
  "updated_date" timestamp NOT NULL,
  "updated_by" text NOT NULL,
  "is_active" integer NULL,
  PRIMARY KEY ("id")
);
-- Create index "idx_is_active" to table: "study"
CREATE INDEX "idx_is_active" ON "accma"."study" ("is_active");
-- Create index "idx_study_alias" to table: "study"
CREATE INDEX "idx_study_alias" ON "accma"."study" ("study_alias");
-- Create "systems" table
CREATE TABLE "accma"."systems" (
  "system_id" integer NOT NULL DEFAULT nextval('accma.systems_seq'::regclass),
  "system_name" text NOT NULL,
  "created_date" timestamp NOT NULL,
  "created_by" text NOT NULL,
  "updated_date" timestamp NOT NULL,
  "updated_by" text NOT NULL,
  "display_name" text NULL,
  PRIMARY KEY ("system_id")
);
-- Create index "idx_systems_system_id" to table: "systems"
CREATE INDEX "idx_systems_system_id" ON "accma"."systems" ("system_id");
-- Create "special_email_domain" table
CREATE TABLE "accma"."special_email_domain" (
  "se_id" smallserial NOT NULL,
  "domain" text NOT NULL,
  "study_id" integer NOT NULL,
  "system_id" integer NOT NULL,
  PRIMARY KEY ("se_id"),
  CONSTRAINT "special_email_domain_study_id_fkey" FOREIGN KEY ("study_id") REFERENCES "accma"."study" ("id") ON UPDATE NO ACTION ON DELETE NO ACTION,
  CONSTRAINT "special_email_domain_system_id_fkey" FOREIGN KEY ("system_id") REFERENCES "accma"."systems" ("system_id") ON UPDATE NO ACTION ON DELETE NO ACTION
);
-- Create "study_meta_data" table
CREATE TABLE "accma"."study_meta_data" (
  "study_meta_data_id" serial NOT NULL,
  "study_id" integer NOT NULL,
  "system_id" integer NOT NULL,
  "created_date" timestamp NOT NULL,
  "created_by" text NOT NULL,
  "updated_date" timestamp NOT NULL,
  "updated_by" text NOT NULL,
  "is_active" integer NOT NULL DEFAULT 0,
  CONSTRAINT "fk_study_meta_data_study" FOREIGN KEY ("study_id") REFERENCES "accma"."study" ("id") ON UPDATE NO ACTION ON DELETE RESTRICT,
  CONSTRAINT "chk_is_active" CHECK (is_active = ANY (ARRAY[0, 1]))
);
