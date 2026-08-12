#!/usr/bin/env bash
set -Eeuo pipefail

umask 077

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
VERIFY_SQL="${SCRIPT_DIR}/sql/db_fast_cutover_verify_ZJ.sql"

SOURCE_DSN="${SOURCE_DSN:-}"
TARGET_DSN="${TARGET_DSN:-}"
JOBS="${JOBS:-4}"
WORK_ROOT="${WORK_ROOT:-${SCRIPT_DIR}/backups/db-fast-cutover-ZJ}"
PGCONNECT_TIMEOUT="${PGCONNECT_TIMEOUT:-10}"

log() {
  printf '[db-fast-cutover-ZJ] %s\n' "$*"
}

die() {
  printf '[db-fast-cutover-ZJ] ERROR: %s\n' "$*" >&2
  exit 1
}

usage() {
  cat <<'EOF'
Usage:
  db-fast-cutover_ZJ.sh preflight
  db-fast-cutover_ZJ.sh restore-base /path/to/sub2api.dump
  db-fast-cutover_ZJ.sh freeze-source
  db-fast-cutover_ZJ.sh cutover
  db-fast-cutover_ZJ.sh verify
  db-fast-cutover_ZJ.sh unfreeze-source

Required environment:
  SOURCE_DSN  libpq DSN for the live source database
  TARGET_DSN  libpq DSN for the pre-restored target database

Safety confirmations:
  ZJ_CONFIRM_TARGET_ID="TARGET:database@address:port"
  ZJ_CONFIRM_FREEZE_SOURCE="FREEZE:database@address:port"
  ZJ_CONFIRM_REPLACE_TARGET="REPLACE:database@address:port"
  ZJ_CONFIRM_UNFREEZE_SOURCE="UNFREEZE:database@address:port"

Optional:
  JOBS=4
  WORK_ROOT=/secure/path
  ALLOW_INVOKING_ASYNC_TASKS=NO
  SAVE_TARGET_BEFORE_REPLACE=NO

Do not put passwords directly in DSNs. Use ~/.pgpass or a libpq service file.
EOF
}

require_command() {
  command -v "$1" >/dev/null 2>&1 || die "required command not found: $1"
}

require_tools() {
  local command_name
  for command_name in psql pg_dump pg_restore diff sha256sum find sort xargs; do
    require_command "${command_name}"
  done
  [[ -f "${VERIFY_SQL}" ]] || die "verification SQL not found: ${VERIFY_SQL}"
  [[ "${JOBS}" =~ ^[1-9][0-9]*$ ]] || die "JOBS must be a positive integer"
}

require_connections() {
  [[ -n "${SOURCE_DSN}" ]] || die "SOURCE_DSN is required"
  [[ -n "${TARGET_DSN}" ]] || die "TARGET_DSN is required"
}

run_psql() {
  local dsn="$1"
  shift
  PGCONNECT_TIMEOUT="${PGCONNECT_TIMEOUT}" psql --dbname="${dsn}" -X --set=ON_ERROR_STOP=1 "$@"
}

query_scalar() {
  local dsn="$1"
  local sql="$2"
  run_psql "${dsn}" --tuples-only --no-align --quiet --command="${sql}" | tr -d '\r' | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//'
}

replica_restore_pgoptions() {
  if [[ -n "${PGOPTIONS:-}" ]]; then
    printf '%s -c session_replication_role=replica' "${PGOPTIONS}"
  else
    printf '%s' '-c session_replication_role=replica'
  fi
}

db_identity() {
  query_scalar "$1" "SELECT current_database() || '@' || COALESCE(inet_server_addr()::text, 'local') || ':' || COALESCE(inet_server_port()::text, 'local')"
}

server_major() {
  query_scalar "$1" "SELECT current_setting('server_version_num')::integer / 10000"
}

database_fingerprint() {
  query_scalar "$1" "SELECT pg_encoding_to_char(encoding) || ':' || datcollate || ':' || datctype FROM pg_database WHERE datname = current_database()"
}

migration_fingerprint() {
  query_scalar "$1" "SELECT count(*)::text || ':' || COALESCE(md5(string_agg(filename || ':' || checksum, E'\\n' ORDER BY filename)), md5('')) FROM schema_migrations"
}

column_fingerprint() {
  query_scalar "$1" "SELECT COALESCE(md5(string_agg(format('%I.%I.%I:%s:%s:%s:%s:%s', table_schema, table_name, column_name, ordinal_position, udt_name, is_nullable, COALESCE(column_default, ''), COALESCE(is_generated, '')), E'\\n' ORDER BY table_schema, table_name, ordinal_position)), md5('')) FROM information_schema.columns WHERE table_schema = 'public'"
}

constraint_fingerprint() {
  query_scalar "$1" "SELECT COALESCE(md5(string_agg(format('%I.%I:%I:%s', n.nspname, c.relname, con.conname, pg_get_constraintdef(con.oid, true)), E'\\n' ORDER BY n.nspname, c.relname, con.conname)), md5('')) FROM pg_constraint con JOIN pg_class c ON c.oid = con.conrelid JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = 'public'"
}

trigger_fingerprint() {
  query_scalar "$1" "SELECT COALESCE(md5(string_agg(format('%I.%I:%I:%s', n.nspname, c.relname, t.tgname, pg_get_triggerdef(t.oid, true)), E'\\n' ORDER BY n.nspname, c.relname, t.tgname)), md5('')) FROM pg_trigger t JOIN pg_class c ON c.oid = t.tgrelid JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = 'public' AND NOT t.tgisinternal"
}

extension_fingerprint() {
  query_scalar "$1" "SELECT COALESCE(md5(string_agg(extname || ':' || extversion, E'\\n' ORDER BY extname)), md5('')) FROM pg_extension"
}

assert_distinct_databases() {
  local source_id target_id
  source_id="$(db_identity "${SOURCE_DSN}")"
  target_id="$(db_identity "${TARGET_DSN}")"
  [[ "${source_id}" != "${target_id}" ]] || die "source and target resolve to the same database: ${source_id}"
}

assert_matching_schema() {
  local source_major target_major source_database target_database source_migrations target_migrations
  local source_columns target_columns source_constraints target_constraints source_triggers target_triggers
  local source_extensions target_extensions
  source_major="$(server_major "${SOURCE_DSN}")"
  target_major="$(server_major "${TARGET_DSN}")"
  [[ "${source_major}" == "${target_major}" ]] || die "PostgreSQL major versions differ: source=${source_major}, target=${target_major}"

  source_database="$(database_fingerprint "${SOURCE_DSN}")"
  target_database="$(database_fingerprint "${TARGET_DSN}")"
  [[ "${source_database}" == "${target_database}" ]] || die "database encoding or locale differs: source=${source_database}, target=${target_database}"

  source_migrations="$(migration_fingerprint "${SOURCE_DSN}")"
  target_migrations="$(migration_fingerprint "${TARGET_DSN}")"
  [[ "${source_migrations}" == "${target_migrations}" ]] || die "schema_migrations differ: source=${source_migrations}, target=${target_migrations}"

  source_columns="$(column_fingerprint "${SOURCE_DSN}")"
  target_columns="$(column_fingerprint "${TARGET_DSN}")"
  [[ "${source_columns}" == "${target_columns}" ]] || die "public column layouts differ: source=${source_columns}, target=${target_columns}"

  source_constraints="$(constraint_fingerprint "${SOURCE_DSN}")"
  target_constraints="$(constraint_fingerprint "${TARGET_DSN}")"
  [[ "${source_constraints}" == "${target_constraints}" ]] || die "public constraints differ: source=${source_constraints}, target=${target_constraints}"

  source_triggers="$(trigger_fingerprint "${SOURCE_DSN}")"
  target_triggers="$(trigger_fingerprint "${TARGET_DSN}")"
  [[ "${source_triggers}" == "${target_triggers}" ]] || die "public user triggers differ: source=${source_triggers}, target=${target_triggers}"

  source_extensions="$(extension_fingerprint "${SOURCE_DSN}")"
  target_extensions="$(extension_fingerprint "${TARGET_DSN}")"
  [[ "${source_extensions}" == "${target_extensions}" ]] || die "PostgreSQL extensions differ: source=${source_extensions}, target=${target_extensions}"
}

assert_target_confirmation() {
  local target_id expected
  target_id="$(db_identity "${TARGET_DSN}")"
  expected="${1}:${target_id}"
  [[ "${2:-}" == "${expected}" ]] || die "confirmation mismatch; set the requested value to: ${expected}"
}

assert_target_idle() {
  local sessions
  sessions="$(query_scalar "${TARGET_DSN}" "SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND pid <> pg_backend_pid() AND backend_type = 'client backend'")"
  [[ "${sessions}" == "0" ]] || die "target still has ${sessions} client session(s); stop the target application and retry"
}

assert_source_idle() {
  local sessions
  sessions="$(query_scalar "${SOURCE_DSN}" "SELECT count(*) FROM pg_stat_activity WHERE datname = current_database() AND pid <> pg_backend_pid() AND backend_type = 'client backend'")"
  [[ "${sessions}" == "0" ]] || die "source still has ${sessions} client session(s); keep the source application stopped and retry"
}

assert_target_replica_restore_privilege() {
  local replica_role
  if ! replica_role="$(PGOPTIONS="$(replica_restore_pgoptions)" PGCONNECT_TIMEOUT="${PGCONNECT_TIMEOUT}" psql --dbname="${TARGET_DSN}" -X --tuples-only --no-align --quiet --set=ON_ERROR_STOP=1 --command="SHOW session_replication_role")"; then
    die "target role cannot set session_replication_role=replica; use a dedicated migration role with the required privilege"
  fi
  replica_role="$(printf '%s' "${replica_role}" | tr -d '\r' | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//')"
  [[ "${replica_role}" == "replica" ]] || die "target restore connection did not enter replica mode; remove conflicting libpq options and retry"
}

assert_source_frozen() {
  local read_only
  read_only="$(query_scalar "${SOURCE_DSN}" "SHOW default_transaction_read_only")"
  [[ "${read_only}" == "on" ]] || die "source database is not frozen; run freeze-source after stopping the source application"
}

assert_no_invoking_async_tasks() {
  local invoking
  invoking="$(query_scalar "${SOURCE_DSN}" "SELECT count(*) FROM async_image_tasks WHERE status = 'invoking'")"
  if [[ "${invoking}" != "0" && "${ALLOW_INVOKING_ASYNC_TASKS:-NO}" != "YES" ]]; then
    die "source has ${invoking} invoking async image task(s); wait for graceful shutdown or explicitly set ALLOW_INVOKING_ASYNC_TASKS=YES after risk review"
  fi
}

preflight() {
  require_tools
  require_connections
  assert_distinct_databases
  assert_matching_schema

  log "source: $(db_identity "${SOURCE_DSN}")"
  log "target: $(db_identity "${TARGET_DSN}")"
  log "PostgreSQL major: $(server_major "${SOURCE_DSN}")"
  log "database fingerprint: $(database_fingerprint "${SOURCE_DSN}")"
  log "migration fingerprint: $(migration_fingerprint "${SOURCE_DSN}")"
  log "column fingerprint: $(column_fingerprint "${SOURCE_DSN}")"
  log "constraint fingerprint: $(constraint_fingerprint "${SOURCE_DSN}")"
  log "trigger fingerprint: $(trigger_fingerprint "${SOURCE_DSN}")"
  log "extension fingerprint: $(extension_fingerprint "${SOURCE_DSN}")"
  log "preflight passed"
}

restore_base() {
  local dump_path="$1"
  require_tools
  require_connections
  [[ -f "${dump_path}" || -d "${dump_path}" ]] || die "base dump not found: ${dump_path}"
  assert_distinct_databases
  assert_target_confirmation "TARGET" "${ZJ_CONFIRM_TARGET_ID:-}"
  assert_target_idle

  pg_restore --list "${dump_path}" >/dev/null
  log "restoring base backup into $(db_identity "${TARGET_DSN}")"
  pg_restore \
    --dbname="${TARGET_DSN}" \
    --clean \
    --if-exists \
    --no-owner \
    --no-privileges \
    --exit-on-error \
    --jobs="${JOBS}" \
    "${dump_path}"
  run_psql "${TARGET_DSN}" --quiet --command="ANALYZE"
  log "base restore completed; run the same Sub2API build once against the isolated target to apply migrations, stop it, then run preflight"
}

freeze_source() {
  local source_id expected
  require_tools
  require_connections
  source_id="$(db_identity "${SOURCE_DSN}")"
  expected="FREEZE:${source_id}"
  [[ "${ZJ_CONFIRM_FREEZE_SOURCE:-}" == "${expected}" ]] || die "confirmation mismatch; set ZJ_CONFIRM_FREEZE_SOURCE=${expected}"

  log "freezing source database ${source_id} and terminating other client sessions"
  run_psql "${SOURCE_DSN}" --quiet <<'SQL'
SELECT format('ALTER DATABASE %I SET default_transaction_read_only = on', current_database()) \gexec
SELECT pg_terminate_backend(pid)
FROM pg_stat_activity
WHERE datname = current_database()
  AND pid <> pg_backend_pid()
  AND backend_type = 'client backend';
SQL
  assert_source_frozen
  log "source is read-only; keep the source application stopped"
}

unfreeze_source() {
  local source_id expected
  require_tools
  require_connections
  source_id="$(db_identity "${SOURCE_DSN}")"
  expected="UNFREEZE:${source_id}"
  [[ "${ZJ_CONFIRM_UNFREEZE_SOURCE:-}" == "${expected}" ]] || die "confirmation mismatch; set ZJ_CONFIRM_UNFREEZE_SOURCE=${expected}"

  log "removing database-level read-only setting from ${source_id}"
  run_psql "${SOURCE_DSN}" --quiet <<'SQL'
SET default_transaction_read_only = off;
SELECT format('ALTER DATABASE %I RESET default_transaction_read_only', current_database()) \gexec
SQL
  log "source is writable for new sessions; only restart the old application when rolling back before target writes are accepted"
}

truncate_target_data() {
  run_psql "${TARGET_DSN}" --quiet <<'SQL'
DO $zj$
DECLARE
  table_list text;
BEGIN
  SELECT string_agg(format('%I.%I', n.nspname, c.relname), ', ' ORDER BY c.oid)
    INTO table_list
  FROM pg_class c
  JOIN pg_namespace n ON n.oid = c.relnamespace
  WHERE n.nspname = 'public'
    AND c.relkind IN ('r', 'p')
    AND NOT EXISTS (
      SELECT 1
      FROM pg_inherits i
      WHERE i.inhrelid = c.oid
    );

  IF table_list IS NULL THEN
    RAISE EXCEPTION 'no public root tables found';
  END IF;

  EXECUTE 'TRUNCATE TABLE ' || table_list || ' RESTART IDENTITY CASCADE';
END
$zj$;
SQL
}

write_verification() {
  local dsn="$1"
  local output="$2"
  run_psql "${dsn}" --tuples-only --no-align --quiet --file="${VERIFY_SQL}" >"${output}"
}

compare_verification() {
  local output_dir="$1"
  write_verification "${SOURCE_DSN}" "${output_dir}/source.verify.txt"
  write_verification "${TARGET_DSN}" "${output_dir}/target.verify.txt"
  if ! diff -u "${output_dir}/source.verify.txt" "${output_dir}/target.verify.txt" >"${output_dir}/verification.diff"; then
    cat "${output_dir}/verification.diff" >&2
    die "source and target verification summaries differ; source remains frozen"
  fi
  log "verification summaries match: ${output_dir}/source.verify.txt"
}

cutover() {
  local target_id expected stamp package_dir data_dir
  preflight
  assert_source_frozen
  assert_source_idle
  assert_no_invoking_async_tasks
  assert_target_idle
  assert_target_replica_restore_privilege

  target_id="$(db_identity "${TARGET_DSN}")"
  expected="REPLACE:${target_id}"
  [[ "${ZJ_CONFIRM_REPLACE_TARGET:-}" == "${expected}" ]] || die "confirmation mismatch; set ZJ_CONFIRM_REPLACE_TARGET=${expected}"

  stamp="$(date -u +%Y%m%dT%H%M%SZ)"
  package_dir="${WORK_ROOT}/cutover-${stamp}"
  data_dir="${package_dir}/data"
  [[ ! -e "${package_dir}" ]] || die "cutover package already exists: ${package_dir}"
  mkdir -p "${package_dir}"

  if [[ "${SAVE_TARGET_BEFORE_REPLACE:-NO}" == "YES" ]]; then
    log "saving target safety dump"
    pg_dump --dbname="${TARGET_DSN}" --format=custom --no-owner --no-privileges --file="${package_dir}/target-before-replace.dump"
  fi

  log "capturing final read-only source snapshot with ${JOBS} job(s)"
  pg_dump \
    --dbname="${SOURCE_DSN}" \
    --format=directory \
    --jobs="${JOBS}" \
    --compress=0 \
    --data-only \
    --schema=public \
    --no-owner \
    --no-privileges \
    --file="${data_dir}"

  {
    printf 'created_at=%s\n' "${stamp}"
    printf 'source=%s\n' "$(db_identity "${SOURCE_DSN}")"
    printf 'target=%s\n' "${target_id}"
    printf 'postgres_major=%s\n' "$(server_major "${SOURCE_DSN}")"
    printf 'database=%s\n' "$(database_fingerprint "${SOURCE_DSN}")"
    printf 'schema_migrations=%s\n' "$(migration_fingerprint "${SOURCE_DSN}")"
    printf 'columns=%s\n' "$(column_fingerprint "${SOURCE_DSN}")"
    printf 'constraints=%s\n' "$(constraint_fingerprint "${SOURCE_DSN}")"
    printf 'triggers=%s\n' "$(trigger_fingerprint "${SOURCE_DSN}")"
    printf 'extensions=%s\n' "$(extension_fingerprint "${SOURCE_DSN}")"
  } >"${package_dir}/manifest.txt"
  find "${data_dir}" -type f -print0 | sort -z | xargs -0 sha256sum >"${package_dir}/data.sha256"

  log "replacing target public data; do not start either application"
  printf 'restore_started_at=%s\nstatus=incomplete\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >"${package_dir}/restore.state"
  truncate_target_data
  PGOPTIONS="$(replica_restore_pgoptions)" pg_restore \
    --dbname="${TARGET_DSN}" \
    --data-only \
    --no-owner \
    --no-privileges \
    --exit-on-error \
    --jobs="${JOBS}" \
    "${data_dir}"
  run_psql "${TARGET_DSN}" --quiet --command="ANALYZE"

  compare_verification "${package_dir}"
  printf 'restore_finished_at=%s\nstatus=verified\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" >"${package_dir}/restore.state"
  log "cutover data load completed: ${package_dir}"
  log "keep source read-only, migrate config/data/Redis as documented, start target, validate, then switch traffic"
}

verify() {
  local stamp output_dir
  require_tools
  require_connections
  assert_distinct_databases
  assert_matching_schema
  stamp="$(date -u +%Y%m%dT%H%M%SZ)"
  output_dir="${WORK_ROOT}/verify-${stamp}"
  mkdir -p "${output_dir}"
  compare_verification "${output_dir}"
}

main() {
  local command_name="${1:-}"
  case "${command_name}" in
    preflight)
      preflight
      ;;
    restore-base)
      [[ $# -eq 2 ]] || die "restore-base requires a dump path"
      restore_base "$2"
      ;;
    freeze-source)
      freeze_source
      ;;
    cutover)
      cutover
      ;;
    verify)
      verify
      ;;
    unfreeze-source)
      unfreeze_source
      ;;
    -h|--help|help|'')
      usage
      ;;
    *)
      usage >&2
      die "unknown command: ${command_name}"
      ;;
  esac
}

main "$@"
