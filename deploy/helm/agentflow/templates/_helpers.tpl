{{- define "agentflow.name" -}}
agentflow
{{- end -}}

{{- define "agentflow.fullname" -}}
{{ .Release.Name }}-agentflow
{{- end -}}
