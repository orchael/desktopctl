export interface DesktopInfo {
  desktop_id: string;
  hostname: string;
  github_owner: string;
  environment: string;
  bridge_port: number;
  repos: string[];
  services: ServiceStatus[];
  novnc_url: string;
  desktop_web_version: string;
}

export interface ServiceStatus {
  name: string;
  active: boolean;
  version?: string;
}
