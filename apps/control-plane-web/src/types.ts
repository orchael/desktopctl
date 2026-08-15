export type LifecycleState =
  | 'creating'
  | 'ready'
  | 'stopped'
  | 'unhealthy'
  | 'failed'
  | 'provisioning_failed'
  | 'terminating'
  | 'terminated';

export interface Desktop {
  desktop_id: string;
  stack_name: string;
  github_owner: string;
  region: string;
  lifecycle_state: LifecycleState;
  instance_id: string;
  hostname: string;
  novnc_url: string;
  ssh_target: string;
  ami_id?: string;
  readiness: string;
  failure_phase?: string;
  failure_message?: string;
  repos?: string[];
  secrets?: string[];
  tailscale_network?: string;
  step_ca_server?: string;
  avd_names?: string[];
  instance_type?: string;
  nested_virt?: boolean;
  market_type?: string;
  stop_reason?: string;
  stopped_at?: string;
  workspace_path?: string;
  created_at: string;
  updated_at: string;
}

export interface DesktopSummary extends Desktop {
  live_state?: string;
}

export interface Readiness {
  ok: boolean;
  environment: string;
  region: string;
  fleet_table: string;
  identity?: string;
  error?: string;
}
