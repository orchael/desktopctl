CREATE TYPE "OrganizationRole" AS ENUM ('OWNER', 'MEMBER');

CREATE TABLE "User" ("id" TEXT PRIMARY KEY, "name" TEXT, "email" TEXT, "emailVerified" TIMESTAMP(3), "image" TEXT);
CREATE UNIQUE INDEX "User_email_key" ON "User"("email");
CREATE TABLE "Account" ("id" TEXT PRIMARY KEY, "userId" TEXT NOT NULL REFERENCES "User"("id") ON DELETE CASCADE, "type" TEXT NOT NULL, "provider" TEXT NOT NULL, "providerAccountId" TEXT NOT NULL, "refresh_token" TEXT, "access_token" TEXT, "expires_at" INTEGER, "token_type" TEXT, "scope" TEXT, "id_token" TEXT, "session_state" TEXT);
CREATE UNIQUE INDEX "Account_provider_providerAccountId_key" ON "Account"("provider", "providerAccountId");
CREATE INDEX "Account_userId_idx" ON "Account"("userId");
CREATE TABLE "Session" ("id" TEXT PRIMARY KEY, "sessionToken" TEXT NOT NULL, "userId" TEXT NOT NULL REFERENCES "User"("id") ON DELETE CASCADE, "expires" TIMESTAMP(3) NOT NULL);
CREATE UNIQUE INDEX "Session_sessionToken_key" ON "Session"("sessionToken");
CREATE INDEX "Session_userId_idx" ON "Session"("userId");
CREATE TABLE "VerificationToken" ("identifier" TEXT NOT NULL, "token" TEXT NOT NULL, "expires" TIMESTAMP(3) NOT NULL);
CREATE UNIQUE INDEX "VerificationToken_identifier_token_key" ON "VerificationToken"("identifier", "token");

CREATE TABLE "Organization" ("id" UUID PRIMARY KEY DEFAULT gen_random_uuid(), "name" TEXT NOT NULL, "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP, "updatedAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE TABLE "Membership" ("organizationId" UUID NOT NULL REFERENCES "Organization"("id") ON DELETE CASCADE, "userId" TEXT NOT NULL REFERENCES "User"("id") ON DELETE CASCADE, "role" "OrganizationRole" NOT NULL DEFAULT 'MEMBER', "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP, PRIMARY KEY ("organizationId", "userId"));
CREATE INDEX "Membership_userId_organizationId_idx" ON "Membership"("userId", "organizationId");
CREATE TABLE "Invitation" ("id" UUID PRIMARY KEY DEFAULT gen_random_uuid(), "organizationId" UUID NOT NULL REFERENCES "Organization"("id") ON DELETE CASCADE, "email" TEXT NOT NULL, "role" "OrganizationRole" NOT NULL DEFAULT 'MEMBER', "invitedById" TEXT NOT NULL REFERENCES "User"("id") ON DELETE CASCADE, "createdAt" TIMESTAMP(3) NOT NULL DEFAULT CURRENT_TIMESTAMP);
CREATE UNIQUE INDEX "Invitation_organizationId_email_key" ON "Invitation"("organizationId", "email");
CREATE INDEX "Invitation_email_idx" ON "Invitation"("email");
CREATE INDEX "Invitation_invitedById_idx" ON "Invitation"("invitedById");

ALTER TABLE "Organization" ENABLE ROW LEVEL SECURITY;
ALTER TABLE "Membership" ENABLE ROW LEVEL SECURITY;
ALTER TABLE "Invitation" ENABLE ROW LEVEL SECURITY;

CREATE FUNCTION app_user_id() RETURNS TEXT LANGUAGE SQL STABLE AS $$ SELECT NULLIF(current_setting('app.current_user_id', true), '') $$;
CREATE FUNCTION app_organization_id() RETURNS UUID LANGUAGE SQL STABLE AS $$ SELECT NULLIF(current_setting('app.current_organization_id', true), '')::UUID $$;
CREATE FUNCTION app_is_member(org UUID) RETURNS BOOLEAN LANGUAGE SQL STABLE SECURITY DEFINER SET search_path = public AS $$ SELECT EXISTS (SELECT 1 FROM "Membership" WHERE "organizationId" = org AND "userId" = app_user_id()) $$;
CREATE FUNCTION app_is_owner(org UUID) RETURNS BOOLEAN LANGUAGE SQL STABLE SECURITY DEFINER SET search_path = public AS $$ SELECT EXISTS (SELECT 1 FROM "Membership" WHERE "organizationId" = org AND "userId" = app_user_id() AND role = 'OWNER') $$;

CREATE POLICY organization_select ON "Organization" FOR SELECT USING (app_is_member(id));
CREATE POLICY organization_update ON "Organization" FOR UPDATE USING (id = app_organization_id() AND app_is_owner(id)) WITH CHECK (id = app_organization_id() AND app_is_owner(id));
CREATE POLICY membership_select ON "Membership" FOR SELECT USING ("organizationId" = app_organization_id() AND app_is_member("organizationId"));
CREATE POLICY membership_self_select ON "Membership" FOR SELECT USING ("userId" = app_user_id());
CREATE POLICY membership_insert ON "Membership" FOR INSERT WITH CHECK ("organizationId" = app_organization_id() AND app_is_owner("organizationId"));
CREATE POLICY membership_update ON "Membership" FOR UPDATE USING ("organizationId" = app_organization_id() AND app_is_owner("organizationId")) WITH CHECK ("organizationId" = app_organization_id() AND app_is_owner("organizationId"));
CREATE POLICY membership_delete ON "Membership" FOR DELETE USING ("organizationId" = app_organization_id() AND app_is_owner("organizationId"));
CREATE POLICY invitation_all ON "Invitation" USING ("organizationId" = app_organization_id() AND app_is_owner("organizationId")) WITH CHECK ("organizationId" = app_organization_id() AND app_is_owner("organizationId"));

CREATE FUNCTION bootstrap_user_organization(target_user TEXT, organization_name TEXT, user_email TEXT)
RETURNS UUID LANGUAGE plpgsql SECURITY DEFINER SET search_path = public AS $$
DECLARE new_org UUID;
BEGIN
  IF EXISTS (SELECT 1 FROM "Membership" WHERE "userId" = target_user) THEN
    SELECT "organizationId" INTO new_org FROM "Membership" WHERE "userId" = target_user ORDER BY "createdAt" LIMIT 1;
    RETURN new_org;
  END IF;
  INSERT INTO "Organization" (name) VALUES (organization_name) RETURNING id INTO new_org;
  INSERT INTO "Membership" ("organizationId", "userId", role) VALUES (new_org, target_user, 'OWNER');
  INSERT INTO "Membership" ("organizationId", "userId", role)
    SELECT "organizationId", target_user, role FROM "Invitation" WHERE lower(email) = lower(user_email)
    ON CONFLICT DO NOTHING;
  DELETE FROM "Invitation" WHERE lower(email) = lower(user_email);
  RETURN new_org;
END $$;

CREATE FUNCTION accept_user_invitations(target_user TEXT, user_email TEXT)
RETURNS VOID LANGUAGE plpgsql SECURITY DEFINER SET search_path = public AS $$
BEGIN
  INSERT INTO "Membership" ("organizationId", "userId", role)
    SELECT "organizationId", target_user, role FROM "Invitation" WHERE lower(email) = lower(user_email)
    ON CONFLICT DO NOTHING;
  DELETE FROM "Invitation" WHERE lower(email) = lower(user_email);
END $$;

CREATE FUNCTION assert_organization_has_owner() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
  IF OLD.role = 'OWNER' AND (TG_OP = 'DELETE' OR NEW.role <> 'OWNER') AND NOT EXISTS (SELECT 1 FROM "Membership" WHERE "organizationId" = OLD."organizationId" AND role = 'OWNER' AND "userId" <> OLD."userId") THEN
    RAISE EXCEPTION 'an organization must always have an owner';
  END IF;
  IF TG_OP = 'DELETE' THEN RETURN OLD; END IF;
  RETURN NEW;
END $$;
CREATE TRIGGER membership_owner_guard BEFORE DELETE OR UPDATE OF role ON "Membership" FOR EACH ROW EXECUTE FUNCTION assert_organization_has_owner();

DO $$ BEGIN
  IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'ai_desktops_app') THEN
    CREATE ROLE ai_desktops_app NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT;
  END IF;
END $$;
GRANT USAGE ON SCHEMA public TO ai_desktops_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO ai_desktops_app;
GRANT EXECUTE ON FUNCTION bootstrap_user_organization(TEXT, TEXT, TEXT), accept_user_invitations(TEXT, TEXT) TO ai_desktops_app;
