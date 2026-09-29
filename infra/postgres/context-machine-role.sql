-- Run once as the owner, outside migrations:
-- psql "$MIGRATION_DATABASE_URL" -v agent_role=context_machine_reader -f infra/postgres/context-machine-role.sql
-- Create this LOGIN with a random password separately using your secret manager.
-- This script never changes the application login or grants access to credentials.
\set ON_ERROR_STOP on
BEGIN;
ALTER ROLE :"agent_role" NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;
ALTER ROLE :"agent_role" SET default_transaction_read_only = on;
GRANT USAGE ON SCHEMA public TO :"agent_role";
GRANT SELECT ON public.client_closes, public.evidence,
  public.current_organization_members, public.current_client_access,
  public.organization_member_access, public.client_access, public.organizations TO :"agent_role";
COMMIT;
-- Readiness rejects a role with table writes, ownership, role memberships or
-- schema CREATE. Do not grant this login the application role.
