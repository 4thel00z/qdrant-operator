{{- define "qdrant-operator.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "qdrant-operator.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{- define "qdrant-operator.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "qdrant-operator.labels" -}}
helm.sh/chart: {{ include "qdrant-operator.chart" . }}
{{ include "qdrant-operator.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "qdrant-operator.selectorLabels" -}}
app.kubernetes.io/name: {{ include "qdrant-operator.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "qdrant-operator.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "qdrant-operator.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{- define "qdrant-operator.peeringName" -}}
{{- default (include "qdrant-operator.fullname" .) .Values.ha.peeringName }}
{{- end }}

{{- define "qdrant-operator.clusterRules" -}}
- apiGroups: ["apiextensions.k8s.io"]
  resources: ["customresourcedefinitions"]
  verbs: ["list", "watch"]
- apiGroups: [""]
  resources: ["persistentvolumes"]
  verbs: ["get"]
- apiGroups: [""]
  resources: ["nodes"]
  verbs: ["list"]
{{- end }}

{{- define "qdrant-operator.peeringRules" -}}
- apiGroups: ["kopf.dev"]
  resources: [{{ ternary "\"kopfpeerings\"" "\"clusterkopfpeerings\"" (not (empty .Values.watchNamespaces)) }}]
  verbs: ["get", "list", "watch", "patch"]
{{- end }}

{{- define "qdrant-operator.namespacedRules" -}}
- apiGroups: ["qdrant.io"]
  resources: ["qdrantclusters", "qdrantbackups", "qdrantbackupschedules", "qdrantrestores", "qdrantcollections", "qdrantaccesskeys", "qdrantmigrations"]
  verbs: ["get", "list", "watch", "patch"]
- apiGroups: ["qdrant.io"]
  resources: ["qdrantbackups"]
  verbs: ["create", "delete"]
- apiGroups: ["qdrant.io"]
  resources: ["qdrantclusters/status", "qdrantbackups/status", "qdrantbackupschedules/status", "qdrantrestores/status", "qdrantcollections/status", "qdrantaccesskeys/status", "qdrantmigrations/status"]
  verbs: ["get", "patch"]
{{ include "qdrant-operator.peeringRules" . }}
- apiGroups: [""]
  resources: ["events"]
  verbs: ["create", "patch"]
- apiGroups: [""]
  resources: ["secrets"]
  verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
- apiGroups: [""]
  resources: ["services", "configmaps", "serviceaccounts"]
  verbs: ["get", "list", "watch", "create", "patch", "delete"]
- apiGroups: [""]
  resources: ["persistentvolumeclaims"]
  verbs: ["list", "watch", "patch", "delete"]
- apiGroups: [""]
  resources: ["persistentvolumeclaims"]
  verbs: ["create"]
- apiGroups: ["snapshot.storage.k8s.io"]
  resources: ["volumesnapshots"]
  verbs: ["create", "get", "list", "delete"]
- apiGroups: [""]
  resources: ["pods"]
  verbs: ["list"]
- apiGroups: [""]
  resources: ["pods/log"]
  verbs: ["get"]
- apiGroups: ["apps"]
  resources: ["statefulsets"]
  verbs: ["get", "list", "watch", "create", "patch", "delete"]
- apiGroups: ["apps"]
  resources: ["statefulsets/status"]
  verbs: ["get"]
- apiGroups: ["policy"]
  resources: ["poddisruptionbudgets"]
  verbs: ["get", "list", "watch", "create", "patch", "delete"]
- apiGroups: ["monitoring.coreos.com"]
  resources: ["servicemonitors"]
  verbs: ["get", "list", "watch", "create", "patch", "delete"]
{{- end }}
