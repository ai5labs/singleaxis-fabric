{{- define "fabric-relay.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "fabric-relay.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}{{ .Release.Name | trunc 63 | trimSuffix "-" }}{{- else -}}{{ printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}{{- end -}}
{{- end -}}
{{- end -}}

{{- define "fabric-relay.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
{{ include "fabric-relay.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: fabric
app.kubernetes.io/component: relay
{{- end -}}

{{- define "fabric-relay.selectorLabels" -}}
app.kubernetes.io/name: {{ include "fabric-relay.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "fabric-relay.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}{{ default (include "fabric-relay.fullname" .) .Values.serviceAccount.name }}{{- else -}}{{ default "default" .Values.serviceAccount.name }}{{- end -}}
{{- end -}}

{{- define "fabric-relay.configName" -}}
{{- printf "%s-config-%s" (include "fabric-relay.fullname" .) (toJson .Values | sha256sum | trunc 10) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "fabric-relay.image" -}}
{{- if .Values.image.digest -}}
{{ printf "%s@%s" .Values.image.repository .Values.image.digest }}
{{- else -}}
{{ printf "%s:%s" .Values.image.repository (.Values.image.tag | default .Chart.AppVersion) }}
{{- end -}}
{{- end -}}

{{/*
Conservative CIDR validation for NetworkPolicy peers. JSON Schema performs the
same basic family/prefix checks; this helper is defense in depth when schema is
skipped. IPv4 additionally must be a canonical network address. Helm templates
do not provide a trustworthy IPv6 integer/CIDR parser, so IPv6 validation is
limited to strict hextet/compression structure and prefix range; the Kubernetes
API remains authoritative for host-bit canonicalization.
*/}}
{{- define "fabric-relay.validateCIDR" -}}
{{- $cidr := .cidr | default "" -}}
{{- $location := .location | default "networkPolicy peer" -}}
{{- $parts := splitList "/" $cidr -}}
{{- if ne (len $parts) 2 -}}{{ fail (printf "fabric-relay: %s CIDR %q is malformed" $location $cidr) }}{{- end -}}
{{- $address := index $parts 0 -}}
{{- $prefixText := index $parts 1 -}}
{{- if not (regexMatch "^[0-9]+$" $prefixText) -}}{{ fail (printf "fabric-relay: %s CIDR %q has an invalid prefix" $location $cidr) }}{{- end -}}
{{- $prefix := atoi $prefixText -}}
{{- if contains ":" $address -}}
{{- if or (lt $prefix 1) (gt $prefix 128) -}}{{ fail (printf "fabric-relay: %s IPv6 CIDR prefix length must be between 1 and 128" $location) }}{{- end -}}
{{- if or (contains ":::" $address) (gt (len (regexFindAll "::" $address -1)) 1) -}}{{ fail (printf "fabric-relay: %s IPv6 CIDR %q has invalid compression" $location $cidr) }}{{- end -}}
{{- if or (and (hasPrefix ":" $address) (not (hasPrefix "::" $address))) (and (hasSuffix ":" $address) (not (hasSuffix "::" $address))) -}}{{ fail (printf "fabric-relay: %s IPv6 CIDR %q has an invalid single leading or trailing colon" $location $cidr) }}{{- end -}}
{{- $hextets := splitList ":" $address -}}
{{- $nonempty := 0 -}}
{{- range $hextet := $hextets -}}
{{- if $hextet -}}
{{- $nonempty = add1 $nonempty -}}
{{- if not (regexMatch "^[0-9A-Fa-f]{1,4}$" $hextet) -}}{{ fail (printf "fabric-relay: %s IPv6 CIDR %q contains an invalid hextet" $location $cidr) }}{{- end -}}
{{- end -}}
{{- end -}}
{{- if contains "::" $address -}}
{{- if ge $nonempty 8 -}}{{ fail (printf "fabric-relay: %s IPv6 CIDR %q has invalid compressed length" $location $cidr) }}{{- end -}}
{{- else -}}
{{- if ne $nonempty 8 -}}{{ fail (printf "fabric-relay: %s IPv6 CIDR %q must contain eight hextets or one compression marker" $location $cidr) }}{{- end -}}
{{- end -}}
{{- else -}}
{{- if or (lt $prefix 1) (gt $prefix 32) -}}{{ fail (printf "fabric-relay: %s IPv4 CIDR prefix length must be between 1 and 32" $location) }}{{- end -}}
{{- $octets := splitList "." $address -}}
{{- if ne (len $octets) 4 -}}{{ fail (printf "fabric-relay: %s IPv4 CIDR %q must contain four octets" $location $cidr) }}{{- end -}}
{{- $fullBytes := div $prefix 8 -}}
{{- $remainder := mod $prefix 8 -}}
{{- $hostUnitByRemainder := list 256 128 64 32 16 8 4 2 -}}
{{- range $index, $octetText := $octets -}}
{{- if not (regexMatch "^(0|[1-9][0-9]{0,2})$" $octetText) -}}{{ fail (printf "fabric-relay: %s IPv4 CIDR %q contains a malformed octet" $location $cidr) }}{{- end -}}
{{- $octet := atoi $octetText -}}
{{- if gt $octet 255 -}}{{ fail (printf "fabric-relay: %s IPv4 CIDR %q contains an out-of-range octet" $location $cidr) }}{{- end -}}
{{- if gt $index $fullBytes -}}
{{- if ne $octet 0 -}}{{ fail (printf "fabric-relay: %s IPv4 CIDR %q is not a canonical network address" $location $cidr) }}{{- end -}}
{{- else if eq $index $fullBytes -}}
{{- if eq $remainder 0 -}}
{{- if ne $octet 0 -}}{{ fail (printf "fabric-relay: %s IPv4 CIDR %q is not a canonical network address" $location $cidr) }}{{- end -}}
{{- else -}}
{{- $hostUnit := index $hostUnitByRemainder $remainder -}}
{{- if ne (mod $octet $hostUnit) 0 -}}{{ fail (printf "fabric-relay: %s IPv4 CIDR %q is not a canonical network address" $location $cidr) }}{{- end -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- define "fabric-relay.validate" -}}
{{- $queueRoot := "/var/lib/fabric-relay" -}}
{{- $queueDir := .Values.queue.directory | default "" -}}
{{- $compactionDir := .Values.queue.compactionDirectory | default "" -}}
{{- range $label, $path := dict "queue.directory" $queueDir "queue.compactionDirectory" $compactionDir -}}
{{- if or (not (hasPrefix (printf "%s/" $queueRoot) $path)) (ne (clean $path) $path) -}}
{{- fail (printf "fabric-relay: %s must resolve beneath the PVC mount %s without traversal" $label $queueRoot) -}}
{{- end -}}
{{- end -}}
{{- if eq $queueDir $compactionDir -}}
{{- fail "fabric-relay: queue.directory and queue.compactionDirectory must be different directories" -}}
{{- end -}}
{{- $authName := .Values.destination.auth.secretRef.name | default "" -}}
{{- $authKey := .Values.destination.auth.secretRef.key | default "" -}}
{{- if and $authName (not $authKey) -}}
{{- fail "fabric-relay: destination.auth.secretRef.key is required when .name is set" -}}
{{- end -}}
{{- $clientSecret := .Values.destination.tls.clientCertificateSecret.name | default "" -}}
{{- $clientCert := .Values.destination.tls.clientCertificateSecret.certKey | default "" -}}
{{- $clientKey := .Values.destination.tls.clientCertificateSecret.privateKeyKey | default "" -}}
{{- if and $clientSecret (or (not $clientCert) (not $clientKey)) -}}
{{- fail "fabric-relay: a TLS client certificate Secret requires certKey and privateKeyKey" -}}
{{- end -}}
{{- $receiverServer := .Values.receiver.tls.serverCertificateSecret.name | default "" -}}
{{- $receiverCert := .Values.receiver.tls.serverCertificateSecret.certKey | default "" -}}
{{- $receiverKey := .Values.receiver.tls.serverCertificateSecret.privateKeyKey | default "" -}}
{{- $receiverCA := .Values.receiver.tls.clientCASecret.name | default "" -}}
{{- $receiverCAKey := .Values.receiver.tls.clientCASecret.key | default "" -}}
{{- if and $receiverServer (or (not $receiverCert) (not $receiverKey)) -}}
{{- fail "fabric-relay: receiver TLS server Secret requires certKey and privateKeyKey" -}}
{{- end -}}
{{- if and $receiverCA (not $receiverCAKey) -}}
{{- fail "fabric-relay: receiver TLS client CA Secret requires key" -}}
{{- end -}}
{{- if ne (not (empty $receiverServer)) (not (empty $receiverCA)) -}}
{{- fail "fabric-relay: receiver TLS server certificate and client CA Secrets must be configured together" -}}
{{- end -}}
{{- if eq .Values.mode "production" -}}
{{- if not .Values.destination.endpoint -}}{{ fail "fabric-relay: production mode requires destination.endpoint" }}{{- end -}}
{{- $endpointURL := urlParse .Values.destination.endpoint -}}
{{- if ne (get $endpointURL "scheme") "https" -}}{{ fail "fabric-relay: production destination must use https" }}{{- end -}}
{{- if not (get $endpointURL "host") -}}{{ fail "fabric-relay: production destination must include a host" }}{{- end -}}
{{- if get $endpointURL "userinfo" -}}{{ fail "fabric-relay: production destination URL must not contain userinfo" }}{{- end -}}
{{- if get $endpointURL "query" -}}{{ fail "fabric-relay: production destination URL must not contain a query" }}{{- end -}}
{{- if get $endpointURL "fragment" -}}{{ fail "fabric-relay: production destination URL must not contain a fragment" }}{{- end -}}
{{- if not (has .Values.destination.endpoint .Values.destination.allowedEndpoints) -}}
{{- fail "fabric-relay: production destination.endpoint must exactly match an entry in destination.allowedEndpoints" -}}
{{- end -}}
{{- if or .Values.destination.tls.insecure .Values.destination.tls.insecureSkipVerify -}}
{{- fail "fabric-relay: production mode requires TLS certificate verification" -}}
{{- end -}}
{{- if not .Values.persistence.enabled -}}{{ fail "fabric-relay: production mode requires persistence.enabled=true" }}{{- end -}}
{{- if .Values.debugExporter.enabled -}}{{ fail "fabric-relay: production mode forbids debugExporter.enabled=true" }}{{- end -}}
{{- if ne .Values.retry.maxElapsedTime "0s" -}}{{ fail "fabric-relay: production mode requires retry.maxElapsedTime=0s" }}{{- end -}}
{{- if or (not $receiverServer) (not $receiverCA) -}}{{ fail "fabric-relay: production mode requires receiver mTLS server certificate and client CA Secret references" }}{{- end -}}
{{- if not .Values.networkPolicy.enabled -}}{{ fail "fabric-relay: production mode requires networkPolicy.enabled=true" }}{{- end -}}
{{- if not .Values.networkPolicy.ingressFrom -}}{{ fail "fabric-relay: production mode requires explicit networkPolicy.ingressFrom selectors" }}{{- end -}}
{{- if not .Values.networkPolicy.egressTo -}}{{ fail "fabric-relay: production mode requires explicit networkPolicy.egressTo selectors or CIDRs" }}{{- end -}}
{{- range $direction, $peers := dict "ingressFrom" .Values.networkPolicy.ingressFrom "egressTo" .Values.networkPolicy.egressTo -}}
{{- range $index, $peer := $peers -}}
{{- if empty $peer -}}{{ fail (printf "fabric-relay: production networkPolicy.%s[%d] must not be an unrestricted empty peer" $direction $index) }}{{- end -}}
{{- range $selectorName := list "namespaceSelector" "podSelector" -}}
{{- if hasKey $peer $selectorName -}}
{{- $selector := get $peer $selectorName -}}
{{- if empty $selector -}}{{ fail (printf "fabric-relay: production networkPolicy.%s[%d].%s must be explicitly restricted" $direction $index $selectorName) }}{{- end -}}
{{- if and (hasKey $selector "matchLabels") (empty (get $selector "matchLabels")) -}}{{ fail (printf "fabric-relay: production networkPolicy.%s[%d].%s.matchLabels must not be empty" $direction $index $selectorName) }}{{- end -}}
{{- if and (hasKey $selector "matchExpressions") (empty (get $selector "matchExpressions")) -}}{{ fail (printf "fabric-relay: production networkPolicy.%s[%d].%s.matchExpressions must not be empty" $direction $index $selectorName) }}{{- end -}}
{{- end -}}
{{- end -}}
{{- if hasKey $peer "ipBlock" -}}
{{- $cidr := get (get $peer "ipBlock") "cidr" | default "" -}}
{{- include "fabric-relay.validateCIDR" (dict "cidr" $cidr "location" (printf "networkPolicy.%s[%d].ipBlock" $direction $index)) -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end -}}
