package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/tabwriter"

	efssdk "github.com/aws/aws-sdk-go-v2/service/efs"
	efstypes "github.com/aws/aws-sdk-go-v2/service/efs/types"
	"github.com/orchael/desktopctl/internal/awsx"
	"github.com/orchael/desktopctl/internal/pulumi"
	"github.com/orchael/desktopctl/internal/store"
	"github.com/spf13/cobra"
)

const (
	workspaceModeLocal = "local"
	workspaceModeEFS   = "efs"
)

var (
	ubuntuUID = int64(1000)
	ubuntuGID = int64(1000)
)

var workspaceNameRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]{1,62}[a-zA-Z0-9]$`)

var workspaceCmd = &cobra.Command{
	Use:   "workspace",
	Short: "Manage retained EFS workspaces",
}

var (
	workspaceCreateName    string
	workspaceCreateOwner   string
	workspaceCreateRepos   []string
	workspaceCreateEnv     string
	workspaceCreatePreview bool
	workspaceListOwner     string
	workspaceListEnv       string
	workspaceListAll       bool
	workspaceStatusEnv     string
	workspaceDeleteEnv     string
	workspaceDetachEnv     string
	workspaceDetachForce   bool
	workspaceAddRepoEnv    string
	workspaceAddRepos      []string
	workspaceRemoveRepoEnv string
	workspaceRemoveRepos   []string
)

var workspaceCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a retained EFS workspace",
	Args:  cobra.NoArgs,
	RunE:  runWorkspaceCreate,
}

var workspaceListCmd = &cobra.Command{
	Use:   "list",
	Short: "List retained EFS workspaces",
	Args:  cobra.NoArgs,
	RunE:  runWorkspaceList,
}

var workspaceStatusCmd = &cobra.Command{
	Use:   "status <name>",
	Short: "Show retained workspace status",
	Args:  cobra.ExactArgs(1),
	RunE:  runWorkspaceStatus,
}

var workspaceDeleteCmd = &cobra.Command{
	Use:   "delete <name>",
	Short: "Delete a detached retained EFS workspace",
	Args:  cobra.ExactArgs(1),
	RunE:  runWorkspaceDelete,
}

var workspaceDetachCmd = &cobra.Command{
	Use:   "detach <name>",
	Short: "Clear a stale retained workspace attachment",
	Long:  "detach clears retained workspace attachment metadata after the operator confirms no live desktop is using the mount.",
	Args:  cobra.ExactArgs(1),
	RunE:  runWorkspaceDetach,
}

var workspaceAddRepoCmd = &cobra.Command{
	Use:   "add-repo <name>",
	Short: "Add repositories to a detached retained workspace",
	Args:  cobra.ExactArgs(1),
	RunE:  runWorkspaceAddRepo,
}

var workspaceRemoveRepoCmd = &cobra.Command{
	Use:   "remove-repo <name>",
	Short: "Remove repositories from a detached retained workspace",
	Args:  cobra.ExactArgs(1),
	RunE:  runWorkspaceRemoveRepo,
}

func init() {
	workspaceCreateCmd.Flags().StringVar(&workspaceCreateName, "name", "", "workspace name, unique within the environment")
	workspaceCreateCmd.Flags().StringVar(&workspaceCreateOwner, "github-owner", "", "GitHub organization or username")
	workspaceCreateCmd.Flags().StringArrayVar(&workspaceCreateRepos, "repo", nil, "GitHub repository assigned to this workspace (repeatable)")
	workspaceCreateCmd.Flags().StringVar(&workspaceCreateEnv, "env", "", "environment (prod|dev), overrides config")
	workspaceCreateCmd.Flags().BoolVar(&workspaceCreatePreview, "preview", false, "preview workspace creation without applying")

	workspaceListCmd.Flags().StringVar(&workspaceListOwner, "github-owner", "", "filter by GitHub organization or username")
	workspaceListCmd.Flags().StringVar(&workspaceListEnv, "env", "", "environment (prod|dev), overrides config")
	workspaceListCmd.Flags().BoolVar(&workspaceListAll, "all", false, "include deleted workspaces")

	workspaceStatusCmd.Flags().StringVar(&workspaceStatusEnv, "env", "", "environment (prod|dev), overrides config")

	workspaceDeleteCmd.Flags().StringVar(&workspaceDeleteEnv, "env", "", "environment (prod|dev), overrides config")

	workspaceDetachCmd.Flags().StringVar(&workspaceDetachEnv, "env", "", "environment (prod|dev), overrides config")
	workspaceDetachCmd.Flags().BoolVar(&workspaceDetachForce, "force", false, "confirm no live desktop is using this workspace")

	workspaceAddRepoCmd.Flags().StringVar(&workspaceAddRepoEnv, "env", "", "environment (prod|dev), overrides config")
	workspaceAddRepoCmd.Flags().StringArrayVar(&workspaceAddRepos, "repo", nil, "GitHub repository to add to this workspace (repeatable)")

	workspaceRemoveRepoCmd.Flags().StringVar(&workspaceRemoveRepoEnv, "env", "", "environment (prod|dev), overrides config")
	workspaceRemoveRepoCmd.Flags().StringArrayVar(&workspaceRemoveRepos, "repo", nil, "GitHub repository to remove from this workspace (repeatable)")

	workspaceCmd.AddCommand(workspaceCreateCmd, workspaceListCmd, workspaceStatusCmd, workspaceDeleteCmd, workspaceDetachCmd, workspaceAddRepoCmd, workspaceRemoveRepoCmd)
	rootCmd.AddCommand(workspaceCmd)
}

type efsWorkspaceClient interface {
	CreateAccessPoint(context.Context, *efssdk.CreateAccessPointInput, ...func(*efssdk.Options)) (*efssdk.CreateAccessPointOutput, error)
	DeleteAccessPoint(context.Context, *efssdk.DeleteAccessPointInput, ...func(*efssdk.Options)) (*efssdk.DeleteAccessPointOutput, error)
}

func runWorkspaceCreate(cmd *cobra.Command, args []string) error {
	if err := requireTools("pulumi"); err != nil {
		return err
	}
	ctx := context.Background()
	if err := requireBackend(ctx); err != nil {
		return err
	}
	env := effectiveWorkspaceEnv(workspaceCreateEnv)
	if err := validateWorkspaceName(workspaceCreateName); err != nil {
		return err
	}
	if strings.TrimSpace(workspaceCreateOwner) == "" {
		return fmt.Errorf("--github-owner is required")
	}
	repos, owner, err := parseAndValidateRepos(workspaceCreateOwner, workspaceCreateRepos)
	if err != nil {
		return err
	}
	workspaceRepos := store.NormalizeWorkspaceRepos(repoStrings(repos))
	fingerprint := store.RepoFingerprint(workspaceRepos)

	backendURL := "s3://" + cfg.Pulumi.BackendBucket
	foundationWorkDir := filepath.Join(cfg.Pulumi.InfraDir, "infra", "pulumi", "foundation")
	runner := &pulumi.Runner{AWSProfile: cfg.AWS.Profile}
	outputs, err := runner.Outputs(ctx, pulumi.FoundationStackRef(backendURL, env, foundationWorkDir))
	if err != nil {
		return fmt.Errorf("read foundation stack outputs (run init-foundation first): %w", err)
	}
	fileSystemID := outputs[pulumi.OutputEFSFileSystemID]
	if fileSystemID == "" {
		return fmt.Errorf("foundation stack output %q is missing; rerun init-foundation to create the environment EFS file system", pulumi.OutputEFSFileSystemID)
	}

	w := &store.Workspace{
		WorkspaceID:     store.WorkspaceRecordID(env, workspaceCreateName),
		WorkspaceName:   workspaceCreateName,
		WorkspaceMode:   workspaceModeEFS,
		Environment:     env,
		GitHubOwner:     owner,
		Repos:           workspaceRepos,
		RepoFingerprint: fingerprint,
		EFSFileSystemID: fileSystemID,
		MountPath:       "/workspace",
		State:           store.WorkspaceStateAvailable,
	}

	if workspaceCreatePreview {
		printWorkspace(os.Stdout, w)
		fmt.Printf("EFS access point path: %s\n", workspaceEFSRootPath(workspaceCreateName))
		return nil
	}

	s, err := openStore(ctx)
	if err != nil {
		return err
	}
	ws, ok := s.(store.WorkspaceStore)
	if !ok {
		return fmt.Errorf("configured store does not support workspaces")
	}
	if existing, err := ws.GetWorkspace(ctx, env, workspaceCreateName); err == nil && existing.State != store.WorkspaceStateDeleted {
		return fmt.Errorf("workspace %q already exists in environment %q", workspaceCreateName, env)
	} else if err != nil && !errors.Is(err, store.ErrNotFound) {
		return err
	}

	awsCfg, err := awsx.LoadConfig(ctx, cfg.AWS.Region, cfg.AWS.Profile)
	if err != nil {
		return fmt.Errorf("AWS config for EFS workspace: %w", err)
	}
	efsClient := efssdk.NewFromConfig(awsCfg)
	ap, err := createWorkspaceAccessPoint(ctx, efsClient, w)
	if err != nil {
		return err
	}
	w.EFSAccessPointID = ap
	if err := ws.CreateWorkspace(ctx, w); err != nil {
		_ = deleteWorkspaceAccessPoint(ctx, efsClient, ap)
		return fmt.Errorf("create workspace record: %w", err)
	}

	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(w)
	}
	printWorkspace(os.Stdout, w)
	return nil
}

func runWorkspaceList(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	env := effectiveWorkspaceEnv(workspaceListEnv)
	s, err := openStore(ctx)
	if err != nil {
		return err
	}
	ws, ok := s.(store.WorkspaceStore)
	if !ok {
		return fmt.Errorf("configured store does not support workspaces")
	}
	workspaces, err := ws.ListWorkspaces(ctx)
	if err != nil {
		return err
	}
	filtered := make([]*store.Workspace, 0, len(workspaces))
	for _, w := range workspaces {
		if w.Environment != env {
			continue
		}
		if workspaceListOwner != "" && !strings.EqualFold(w.GitHubOwner, workspaceListOwner) {
			continue
		}
		if !workspaceListAll && w.State == store.WorkspaceStateDeleted {
			continue
		}
		filtered = append(filtered, w)
	}
	sort.Slice(filtered, func(i, j int) bool {
		return filtered[i].WorkspaceName < filtered[j].WorkspaceName
	})
	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(filtered)
	}
	if len(filtered) == 0 {
		fmt.Println("No workspaces found.")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tSTATE\tOWNER\tENV\tATTACHED DESKTOP\tREPOS")
	for _, workspace := range filtered {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			workspace.WorkspaceName,
			workspace.State,
			workspace.GitHubOwner,
			workspace.Environment,
			workspace.AttachedDesktopID,
			strings.Join(workspace.Repos, ","),
		)
	}
	return w.Flush()
}

func runWorkspaceStatus(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	workspace, err := getWorkspaceFromStore(ctx, effectiveWorkspaceEnv(workspaceStatusEnv), args[0])
	if err != nil {
		return err
	}
	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(workspace)
	}
	printWorkspace(os.Stdout, workspace)
	return nil
}

func runWorkspaceDelete(cmd *cobra.Command, args []string) error {
	ctx := context.Background()
	env := effectiveWorkspaceEnv(workspaceDeleteEnv)
	workspace, err := getWorkspaceFromStore(ctx, env, args[0])
	if err != nil {
		return err
	}
	if workspace.AttachedDesktopID != "" {
		return fmt.Errorf("workspace %q is attached to desktop %q", workspace.WorkspaceName, workspace.AttachedDesktopID)
	}
	s, err := openStore(ctx)
	if err != nil {
		return err
	}
	ws, ok := s.(store.WorkspaceStore)
	if !ok {
		return fmt.Errorf("configured store does not support workspaces")
	}
	if workspace.State != store.WorkspaceStateDeleted {
		if err := ws.DeleteWorkspace(ctx, env, args[0]); err != nil {
			return err
		}
	}
	awsCfg, err := awsx.LoadConfig(ctx, cfg.AWS.Region, cfg.AWS.Profile)
	if err != nil {
		return fmt.Errorf("workspace marked deleted, but failed to load AWS config for EFS access point deletion: %w", err)
	}
	if workspace.EFSAccessPointID != "" {
		if err := deleteWorkspaceAccessPoint(ctx, efssdk.NewFromConfig(awsCfg), workspace.EFSAccessPointID); err != nil {
			return fmt.Errorf("workspace marked deleted, but failed to delete EFS access point: %w", err)
		}
	}
	fmt.Printf("Workspace %s deleted.\n", args[0])
	return nil
}

func runWorkspaceDetach(cmd *cobra.Command, args []string) error {
	if !workspaceDetachForce {
		return fmt.Errorf("workspace detach requires --force after confirming no live desktop is using the mount")
	}
	ctx := context.Background()
	env := effectiveWorkspaceEnv(workspaceDetachEnv)
	s, err := openStore(ctx)
	if err != nil {
		return err
	}
	ws, ok := s.(store.WorkspaceStore)
	if !ok {
		return fmt.Errorf("configured store does not support workspaces")
	}
	if err := ws.DetachWorkspace(ctx, env, args[0], ""); err != nil {
		return err
	}
	fmt.Printf("Workspace %s detached.\n", args[0])
	return nil
}

func runWorkspaceAddRepo(cmd *cobra.Command, args []string) error {
	return runWorkspaceRepoMutation(context.Background(), effectiveWorkspaceEnv(workspaceAddRepoEnv), args[0], workspaceAddRepos, false)
}

func runWorkspaceRemoveRepo(cmd *cobra.Command, args []string) error {
	return runWorkspaceRepoMutation(context.Background(), effectiveWorkspaceEnv(workspaceRemoveRepoEnv), args[0], workspaceRemoveRepos, true)
}

func runWorkspaceRepoMutation(ctx context.Context, env, name string, rawRepos []string, remove bool) error {
	if len(rawRepos) == 0 {
		return fmt.Errorf("--repo is required")
	}
	if err := validateWorkspaceName(name); err != nil {
		return err
	}
	s, err := openStore(ctx)
	if err != nil {
		return err
	}
	ws, ok := s.(store.WorkspaceStore)
	if !ok {
		return fmt.Errorf("configured store does not support workspaces")
	}
	workspace, err := ws.GetWorkspace(ctx, env, name)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("workspace %q not found in environment %q", name, env)
		}
		return err
	}
	newRepos, err := workspaceRepoMutation(workspace, rawRepos, remove)
	if err != nil {
		return err
	}
	if err := ws.UpdateDetachedWorkspaceRepos(ctx, env, name, newRepos, store.RepoFingerprint(newRepos)); err != nil {
		if errors.Is(err, store.ErrWorkspaceAttached) {
			return workspaceAttachedError(ctx, ws, env, name, workspace)
		}
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("workspace %q not found in environment %q", name, env)
		}
		return err
	}
	updated, err := ws.GetWorkspace(ctx, env, name)
	if err != nil {
		return err
	}
	if jsonOut {
		return json.NewEncoder(os.Stdout).Encode(updated)
	}
	printWorkspace(os.Stdout, updated)
	return nil
}

func workspaceRepoMutation(w *store.Workspace, rawRepos []string, remove bool) ([]string, error) {
	if w.State == store.WorkspaceStateDeleted {
		return nil, fmt.Errorf("workspace %q not found in environment %q", w.WorkspaceName, w.Environment)
	}
	if w.AttachedDesktopID != "" {
		return nil, fmt.Errorf("workspace %q is attached to desktop %q; detach or terminate the desktop before changing repos", w.WorkspaceName, w.AttachedDesktopID)
	}
	repos, _, err := parseAndValidateRepos(w.GitHubOwner, rawRepos)
	if err != nil {
		return nil, err
	}
	mutationRepos := store.NormalizeWorkspaceRepos(repoStrings(repos))
	if len(mutationRepos) == 0 {
		return nil, fmt.Errorf("--repo is required")
	}
	current := store.NormalizeWorkspaceRepos(w.Repos)
	if remove {
		return removeWorkspaceRepos(current, mutationRepos)
	}
	return addWorkspaceRepos(current, mutationRepos), nil
}

func addWorkspaceRepos(current, additions []string) []string {
	merged := append([]string(nil), current...)
	merged = append(merged, additions...)
	return store.NormalizeWorkspaceRepos(merged)
}

func removeWorkspaceRepos(current, removals []string) ([]string, error) {
	removeSet := make(map[string]struct{}, len(removals))
	for _, repo := range removals {
		removeSet[repo] = struct{}{}
	}
	out := make([]string, 0, len(current))
	for _, repo := range current {
		if _, ok := removeSet[repo]; ok {
			delete(removeSet, repo)
			continue
		}
		out = append(out, repo)
	}
	if len(removeSet) > 0 {
		missing := make([]string, 0, len(removeSet))
		for repo := range removeSet {
			missing = append(missing, repo)
		}
		sort.Strings(missing)
		return nil, fmt.Errorf("workspace does not contain repo(s): %s", strings.Join(missing, ", "))
	}
	return out, nil
}

func workspaceAttachedError(ctx context.Context, ws store.WorkspaceStore, env, name string, previous *store.Workspace) error {
	attachedID := previous.AttachedDesktopID
	if current, err := ws.GetWorkspace(ctx, env, name); err == nil && current.AttachedDesktopID != "" {
		attachedID = current.AttachedDesktopID
	}
	if attachedID == "" {
		return fmt.Errorf("workspace %q is attached; detach or terminate the desktop before changing repos", name)
	}
	return fmt.Errorf("workspace %q is attached to desktop %q; detach or terminate the desktop before changing repos", name, attachedID)
}

func getWorkspaceFromStore(ctx context.Context, env, name string) (*store.Workspace, error) {
	s, err := openStore(ctx)
	if err != nil {
		return nil, err
	}
	ws, ok := s.(store.WorkspaceStore)
	if !ok {
		return nil, fmt.Errorf("configured store does not support workspaces")
	}
	w, err := ws.GetWorkspace(ctx, env, name)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, fmt.Errorf("workspace %q not found in environment %q", name, env)
		}
		return nil, err
	}
	return w, nil
}

func createWorkspaceAccessPoint(ctx context.Context, client efsWorkspaceClient, w *store.Workspace) (string, error) {
	token := workspaceClientToken(w.Environment, w.WorkspaceName)
	out, err := client.CreateAccessPoint(ctx, &efssdk.CreateAccessPointInput{
		ClientToken:  &token,
		FileSystemId: &w.EFSFileSystemID,
		PosixUser: &efstypes.PosixUser{
			Gid: &ubuntuGID,
			Uid: &ubuntuUID,
		},
		RootDirectory: &efstypes.RootDirectory{
			Path: awsString(workspaceEFSRootPath(w.WorkspaceName)),
			CreationInfo: &efstypes.CreationInfo{
				OwnerGid:    &ubuntuGID,
				OwnerUid:    &ubuntuUID,
				Permissions: awsString("0755"),
			},
		},
		Tags: workspaceEFSTags(w),
	})
	if err != nil {
		return "", fmt.Errorf("create EFS access point: %w", err)
	}
	if out.AccessPointId == nil || *out.AccessPointId == "" {
		return "", fmt.Errorf("create EFS access point: AWS returned empty access point ID")
	}
	return *out.AccessPointId, nil
}

func deleteWorkspaceAccessPoint(ctx context.Context, client efsWorkspaceClient, accessPointID string) error {
	_, err := client.DeleteAccessPoint(ctx, &efssdk.DeleteAccessPointInput{
		AccessPointId: &accessPointID,
	})
	if err != nil {
		var notFound *efstypes.AccessPointNotFound
		if errors.As(err, &notFound) {
			return nil
		}
		return fmt.Errorf("delete EFS access point %s: %w", accessPointID, err)
	}
	return nil
}

func workspaceEFSTags(w *store.Workspace) []efstypes.Tag {
	repoDigest := sha256.Sum256([]byte(w.RepoFingerprint))
	return []efstypes.Tag{
		{Key: awsString("Name"), Value: awsString("ai-desktops-" + w.Environment + "-" + w.WorkspaceName)},
		{Key: awsString("managed-by"), Value: awsString("ai-desktops")},
		{Key: awsString("environment"), Value: awsString(w.Environment)},
		{Key: awsString("workspace-name"), Value: awsString(w.WorkspaceName)},
		{Key: awsString("github-owner"), Value: awsString(w.GitHubOwner)},
		{Key: awsString("repo-fingerprint"), Value: awsString(hex.EncodeToString(repoDigest[:]))},
	}
}

func awsString(value string) *string {
	return &value
}

func workspaceEFSRootPath(name string) string {
	return "/workspaces/" + name
}

func workspaceClientToken(env, name string) string {
	sum := sha256.Sum256([]byte(env + "\x00" + name))
	return "ai-desktops-" + env + "-" + hex.EncodeToString(sum[:])[:16]
}

func validateWorkspaceName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("--name is required")
	}
	if !workspaceNameRe.MatchString(name) {
		return fmt.Errorf("workspace name %q must be 3-64 characters and contain only letters, numbers, dots, underscores, and hyphens", name)
	}
	return nil
}

func effectiveWorkspaceEnv(flagValue string) string {
	if flagValue != "" {
		return flagValue
	}
	return cfg.Fleet.Environment
}

func printWorkspace(out *os.File, w *store.Workspace) {
	fmt.Fprintf(out, "Workspace    : %s\n", w.WorkspaceName)
	fmt.Fprintf(out, "State        : %s\n", w.State)
	fmt.Fprintf(out, "Mode         : %s\n", w.WorkspaceMode)
	fmt.Fprintf(out, "Environment  : %s\n", w.Environment)
	fmt.Fprintf(out, "Owner        : %s\n", w.GitHubOwner)
	fmt.Fprintf(out, "Mount path   : %s\n", w.MountPath)
	fmt.Fprintf(out, "EFS FS ID    : %s\n", w.EFSFileSystemID)
	fmt.Fprintf(out, "EFS AP ID    : %s\n", w.EFSAccessPointID)
	if w.AttachedDesktopID != "" {
		fmt.Fprintf(out, "Attached ID  : %s\n", w.AttachedDesktopID)
	}
	if w.AttachedDesktopName != "" {
		fmt.Fprintf(out, "Attached name: %s\n", w.AttachedDesktopName)
	}
	if len(w.Repos) > 0 {
		fmt.Fprintf(out, "Repos        : %s\n", strings.Join(w.Repos, ", "))
	}
	fmt.Fprintf(out, "Created      : %s\n", w.CreatedAt)
	fmt.Fprintf(out, "Updated      : %s\n", w.UpdatedAt)
}
