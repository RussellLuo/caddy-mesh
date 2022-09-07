{{/* vim: set filetype=mustache: */}}

{{/*
Define the templated controller image with tag.
*/}}
{{- define "caddyMesh.controllerImage" -}}
    {{- printf "%s:%s" .Values.controller.image.name ( .Values.controller.image.tag | default .Chart.AppVersion ) -}}
{{- end -}}

{{/*
Define the templated proxy image with tag.
*/}}
{{- define "caddyMesh.proxyImage" -}}
    {{- printf "%s:%s" .Values.proxy.image.name ( .Values.proxy.image.tag | default "2.5.2" ) -}}
{{- end -}}
