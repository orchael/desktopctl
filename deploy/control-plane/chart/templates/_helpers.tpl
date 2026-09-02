{{- define "control-plane.name" -}}ai-desktops-control-plane{{- end }}
{{- define "control-plane.labels" -}}
app.kubernetes.io/name: {{ include "control-plane.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}
{{- define "control-plane.secretName" -}}{{ .Values.secrets.name }}{{- end }}
