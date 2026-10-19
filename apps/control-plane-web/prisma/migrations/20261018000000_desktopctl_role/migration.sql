-- Keep the applied initial migration immutable. Grant the new runtime role
-- access for fresh desktopctl installations and compatible existing databases.
DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'desktopctl_app') THEN
    CREATE ROLE desktopctl_app NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT;
  END IF;
END $$;
GRANT USAGE ON SCHEMA public TO desktopctl_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO desktopctl_app;
GRANT EXECUTE ON FUNCTION bootstrap_user_organization(TEXT, TEXT, TEXT), accept_user_invitations(TEXT, TEXT) TO desktopctl_app;
