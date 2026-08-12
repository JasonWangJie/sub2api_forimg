\set ON_ERROR_STOP on

SET statement_timeout = '30min';
SET lock_timeout = '10s';
SET TIME ZONE 'UTC';

SELECT 'schema_migrations|'
    || count(*)::text || '|'
    || COALESCE(md5(string_agg(filename || ':' || checksum, E'\n' ORDER BY filename)), md5(''))
FROM schema_migrations;

SELECT 'users|'
    || count(*)::text || '|'
    || COALESCE(sum(balance), 0)::text || '|'
    || COALESCE(sum(frozen_balance), 0)::text || '|'
    || COALESCE(sum(total_recharged), 0)::text || '|'
    || count(*) FILTER (WHERE status = 'active' AND deleted_at IS NULL)::text
FROM users;

SELECT 'api_keys|'
    || count(*)::text || '|'
    || count(*) FILTER (WHERE status = 'active' AND deleted_at IS NULL)::text || '|'
    || COALESCE(sum(quota_used), 0)::text || '|'
    || COALESCE(sum(usage_5h), 0)::text || '|'
    || COALESCE(sum(usage_1d), 0)::text || '|'
    || COALESCE(sum(usage_7d), 0)::text
FROM api_keys;

SELECT 'user_subscriptions|'
    || count(*)::text || '|'
    || count(*) FILTER (WHERE status = 'active' AND deleted_at IS NULL)::text || '|'
    || COALESCE(sum(daily_usage_usd), 0)::text || '|'
    || COALESCE(sum(weekly_usage_usd), 0)::text || '|'
    || COALESCE(sum(monthly_usage_usd), 0)::text
FROM user_subscriptions;

SELECT 'user_platform_quotas|'
    || count(*)::text || '|'
    || COALESCE(sum(daily_usage_usd), 0)::text || '|'
    || COALESCE(sum(weekly_usage_usd), 0)::text || '|'
    || COALESCE(sum(monthly_usage_usd), 0)::text
FROM user_platform_quotas;

SELECT 'usage_logs|'
    || count(*)::text || '|'
    || COALESCE(max(id), 0)::text || '|'
    || COALESCE(sum(actual_cost), 0)::text || '|'
    || COALESCE(max(created_at)::text, '')
FROM usage_logs;

SELECT 'usage_billing_dedup|'
    || count(*)::text || '|'
    || COALESCE(max(id), 0)::text || '|'
    || COALESCE(max(created_at)::text, '')
FROM usage_billing_dedup;

SELECT 'usage_billing_dedup_archive|'
    || count(*)::text || '|'
    || COALESCE(max(created_at)::text, '')
FROM usage_billing_dedup_archive;

SELECT 'payment_orders|'
    || count(*)::text || '|'
    || COALESCE(sum(amount), 0)::text || '|'
    || COALESCE(sum(pay_amount), 0)::text || '|'
    || COALESCE(sum(refund_amount), 0)::text || '|'
    || COALESCE((SELECT string_agg(status || ':' || status_count, ',' ORDER BY status)
                 FROM (SELECT status, count(*)::text AS status_count FROM payment_orders GROUP BY status) s), '')
FROM payment_orders;

SELECT 'payment_audit_logs|'
    || count(*)::text || '|'
    || COALESCE(max(id), 0)::text
FROM payment_audit_logs;

SELECT 'affiliate_ledger|'
    || count(*)::text || '|'
    || COALESCE(sum(amount), 0)::text || '|'
    || COALESCE(max(id), 0)::text
FROM user_affiliate_ledger;

SELECT 'async_image_tasks|'
    || count(*)::text || '|'
    || COALESCE(sum(actual_cost), 0)::text || '|'
    || COALESCE((SELECT string_agg(status || ':' || status_count, ',' ORDER BY status)
                 FROM (SELECT status, count(*)::text AS status_count FROM async_image_tasks GROUP BY status) s), '') || '|'
    || COALESCE((SELECT string_agg(billing_status || ':' || status_count, ',' ORDER BY billing_status)
                 FROM (SELECT billing_status, count(*)::text AS status_count FROM async_image_tasks GROUP BY billing_status) s), '')
FROM async_image_tasks;

SELECT 'batch_image_jobs|'
    || count(*)::text || '|'
    || COALESCE(sum(actual_cost), 0)::text || '|'
    || COALESCE(sum(hold_amount), 0)::text || '|'
    || COALESCE((SELECT string_agg(status || ':' || status_count, ',' ORDER BY status)
                 FROM (SELECT status, count(*)::text AS status_count FROM batch_image_jobs GROUP BY status) s), '')
FROM batch_image_jobs;

SELECT 'image_storage_objects|'
    || count(*)::text || '|'
    || COALESCE(sum(byte_size), 0)::text || '|'
    || count(*) FILTER (WHERE state = 'active')::text
FROM image_storage_objects;

SELECT 'settings|'
    || count(*)::text || '|'
    || COALESCE(md5(string_agg(key || ':' || COALESCE(value, '<NULL>'), E'\n' ORDER BY key)), md5(''))
FROM settings;

DO $zj$
DECLARE
    sequence_row record;
    sequence_value numeric;
    maximum_value numeric;
BEGIN
    FOR sequence_row IN
        SELECT sequence_ns.nspname AS sequence_schema,
               sequence_class.relname AS sequence_name,
               table_ns.nspname AS table_schema,
               table_class.relname AS table_name,
               attribute.attname AS column_name
        FROM pg_class sequence_class
        JOIN pg_namespace sequence_ns ON sequence_ns.oid = sequence_class.relnamespace
        JOIN pg_depend dependency
          ON dependency.classid = 'pg_class'::regclass
         AND dependency.objid = sequence_class.oid
         AND dependency.deptype IN ('a', 'i')
        JOIN pg_class table_class ON table_class.oid = dependency.refobjid
        JOIN pg_namespace table_ns ON table_ns.oid = table_class.relnamespace
        JOIN pg_attribute attribute
          ON attribute.attrelid = table_class.oid
         AND attribute.attnum = dependency.refobjsubid
        WHERE sequence_class.relkind = 'S'
          AND sequence_ns.nspname = 'public'
          AND table_ns.nspname = 'public'
    LOOP
        EXECUTE format('SELECT last_value FROM %I.%I', sequence_row.sequence_schema, sequence_row.sequence_name)
           INTO sequence_value;
        EXECUTE format('SELECT COALESCE(max(%I), 0) FROM %I.%I', sequence_row.column_name, sequence_row.table_schema, sequence_row.table_name)
           INTO maximum_value;
        IF sequence_value < maximum_value THEN
            RAISE EXCEPTION 'sequence %.% is behind %.%: % < %',
                sequence_row.sequence_schema,
                sequence_row.sequence_name,
                sequence_row.table_schema,
                sequence_row.table_name,
                sequence_value,
                maximum_value;
        END IF;
    END LOOP;
END
$zj$;

SELECT 'sequence_ownership|ok';
