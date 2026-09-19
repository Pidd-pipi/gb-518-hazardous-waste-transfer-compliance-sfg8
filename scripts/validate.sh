#!/usr/bin/env sh
set -eu

project_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$project_root"
set -a
if [ -f .env ]; then . ./.env; else . ./.env.example; fi
set +a

command -v jq >/dev/null 2>&1 || { echo "jq is required for API validation" >&2; exit 1; }
(cd backend && go test ./... && go build ./...)
(cd frontend && npm ci --no-audit --no-fund && npm run typecheck && npm run build)
docker compose config --quiet
docker compose down -v --remove-orphans
docker compose up -d --build

cleanup() { docker compose down -v --remove-orphans; }
if [ "${KEEP_RUNNING:-0}" = "1" ]; then
  trap cleanup INT TERM
else
  trap cleanup EXIT INT TERM
fi

backend_url="http://127.0.0.1:${BACKEND_PORT:-19518}"
frontend_url="http://127.0.0.1:${FRONTEND_PORT:-18518}"
i=0
until curl -fsS "$backend_url/healthz" | jq -e '.data.status == "ok" and .data.database == "ready" and .data.redis == "ready"' >/dev/null; do
  i=$((i+1))
  [ "$i" -lt 60 ] || { docker compose logs; exit 1; }
  sleep 2
done
curl -fsS "$frontend_url/" >/dev/null

login() {
  curl -fsS -X POST "$backend_url/api/auth/login" -H 'Content-Type: application/json' \
    -d "{\"username\":\"$1\",\"password\":\"Admin123!\"}" | jq -er '.data.token'
}

admin_token=$(login admin)
viewer_token=$(login viewer)
operator_token=$(login operator)
reviewer_token=$(login reviewer)

curl -fsS "$backend_url/api/session" -H "Authorization: Bearer $admin_token" | jq -e '.data.role == "admin" and (.data.requestId | length > 0)' >/dev/null
curl -fsS "$backend_url/api/runtime" -H "Authorization: Bearer $admin_token" | jq -e '.data.appName and .data.databaseDriver == "postgres" and .data.redisEnabled' >/dev/null

for resource in generators carriers manifests checks; do
  curl -fsS "$backend_url/api/$resource?page=1&pageSize=20" -H "Authorization: Bearer $viewer_token" | jq -e '.data | type == "array"' >/dev/null
done

now=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
stamp=$(date '+%s')
manifest_code="TM-VALIDATE-$stamp"
manifest_payload=$(jq -nc --arg code "$manifest_code" --arg now "$now" '{
  code:$code,name:"空卷验收联单",description:"Compose API validation",
  generatorCode:"WG-001",carrierCode:"CP-002",wasteCode:"HW08-900-249-08",quantityKg:680.5,destination:"合规处置中心 A",
  facility:"东区危废暂存区",owner:"operator",category:"危废转运",riskLevel:"medium",metricValue:68,metricUnit:"score",
  effectiveAt:$now,evidence:"minio://evidence/validation/manifest.pdf",relatedCode:"VALIDATION"
}')

viewer_status=$(curl -sS -o /dev/null -w '%{http_code}' -X POST "$backend_url/api/manifests" \
  -H "Authorization: Bearer $viewer_token" -H 'Content-Type: application/json' -d "$manifest_payload")
[ "$viewer_status" = "403" ]

created=$(curl -fsS -X POST "$backend_url/api/manifests" -H "Authorization: Bearer $operator_token" \
  -H 'X-Request-ID: validation-manifest-create' -H 'Content-Type: application/json' -d "$manifest_payload")
manifest_id=$(printf '%s' "$created" | jq -er '.data.id')
manifest_version=$(printf '%s' "$created" | jq -er '.data.version')
printf '%s' "$created" | jq -e '.data.status == "draft" and .data.generatorCode == "WG-001" and .data.carrierCode == "CP-002"' >/dev/null

skip_status=$(curl -sS -o /dev/null -w '%{http_code}' -X POST "$backend_url/api/manifests/$manifest_id/transition" \
  -H "Authorization: Bearer $operator_token" -H 'Content-Type: application/json' \
  -d "{\"status\":\"in_transit\",\"expectedVersion\":$manifest_version,\"reason\":\"skip must be rejected\"}")
[ "$skip_status" = "422" ]

submitted=$(curl -fsS -X POST "$backend_url/api/manifests/$manifest_id/transition" \
  -H "Authorization: Bearer $operator_token" -H 'X-Request-ID: validation-manifest-submit' -H 'Content-Type: application/json' \
  -d "{\"status\":\"submitted\",\"expectedVersion\":$manifest_version,\"reason\":\"generator and carrier evidence verified\"}")
printf '%s' "$submitted" | jq -e '.data.status == "submitted" and .data.version == 2' >/dev/null
# 提交即冻结资质快照：证照编号、状态、有效期与车辆数随联单保存。
printf '%s' "$submitted" | jq -e '.data.snapshotVersion == 1
  and .data.generatorPermitNumber == "PERMIT-WG-001" and .data.generatorPermitStatus == "active"
  and (.data.generatorPermitExpiresAt | length > 0)
  and .data.carrierLicenseNumber == "CARRIER-LIC-002" and .data.carrierLicenseStatus == "verified"
  and (.data.carrierLicenseExpiresAt | length > 0) and .data.carrierVehicleCount == 16
  and .data.snapshotInvalidReason == ""' >/dev/null

# 重复提交只能成功一次。
duplicate_status=$(curl -sS -o /dev/null -w '%{http_code}' -X POST "$backend_url/api/manifests/$manifest_id/transition" \
  -H "Authorization: Bearer $operator_token" -H 'Content-Type: application/json' \
  -d '{"status":"submitted","expectedVersion":2,"reason":"duplicate submit must be rejected"}')
[ "$duplicate_status" = "422" ]

# 刷新后可回读冻结快照。
snapshot_view=$(curl -fsS "$backend_url/api/manifests/snapshots?codes=$manifest_code" -H "Authorization: Bearer $viewer_token")
printf '%s' "$snapshot_view" | jq -e '.data[0].snapshotVersion == 1 and .data[0].invalidReason == "" and .data[0].carrierVehicleCount == 16' >/dev/null

# 发运前产废许可实时停用：整单拒绝且状态不变，失效原因随快照留存。
generator=$(curl -fsS "$backend_url/api/generators?search=WG-001" -H "Authorization: Bearer $reviewer_token")
generator_id=$(printf '%s' "$generator" | jq -er '.data[0].id')
generator_version=$(printf '%s' "$generator" | jq -er '.data[0].version')
curl -fsS -X POST "$backend_url/api/generators/$generator_id/transition" -H "Authorization: Bearer $reviewer_token" \
  -H 'Content-Type: application/json' -d "{\"status\":\"suspended\",\"expectedVersion\":$generator_version,\"reason\":\"validation suspends the permit\"}" >/dev/null
blocked_dispatch=$(curl -sS -o /dev/null -w '%{http_code}' -X POST "$backend_url/api/manifests/$manifest_id/transition" \
  -H "Authorization: Bearer $operator_token" -H 'X-Request-ID: validation-dispatch-blocked' -H 'Content-Type: application/json' \
  -d '{"status":"in_transit","expectedVersion":2,"reason":"suspended permit must block dispatch"}')
[ "$blocked_dispatch" = "422" ]
manifest_now=$(curl -fsS "$backend_url/api/manifests/$manifest_id" -H "Authorization: Bearer $viewer_token")
printf '%s' "$manifest_now" | jq -e '.data.status == "submitted" and .data.snapshotVersion == 1
  and (.data.snapshotInvalidReason | length > 0)
  and .data.generatorPermitNumber == "PERMIT-WG-001" and .data.generatorPermitStatus == "active"' >/dev/null
manifest_version=$(printf '%s' "$manifest_now" | jq -er '.data.version')

# 证照变化不得改写历史快照；携带失效原因时核验不得通过。
snapshot_view=$(curl -fsS "$backend_url/api/manifests/snapshots?codes=$manifest_code" -H "Authorization: Bearer $viewer_token")
printf '%s' "$snapshot_view" | jq -e '.data[0].snapshotVersion == 1 and (.data[0].invalidReason | length > 0)' >/dev/null
blocked_check_code="CC-BLOCKED-$stamp"
blocked_check_payload=$(jq -nc --arg code "$blocked_check_code" --arg manifest "$manifest_code" --arg now "$now" '{
  code:$code,name:"失效快照拦截核验",description:"Snapshot invalid reason must block the pass",manifestCode:$manifest,
  checklist:"产废许可、承运资质、联单数量、处置去向",decisionBasis:"",facility:"复核中心",owner:"reviewer",
  category:"联单复核",riskLevel:"medium",metricValue:60,metricUnit:"score",effectiveAt:$now,
  evidence:"minio://evidence/validation/blocked-check.pdf",relatedCode:$manifest
}')
blocked_check=$(curl -fsS -X POST "$backend_url/api/checks" -H "Authorization: Bearer $operator_token" -H 'Content-Type: application/json' -d "$blocked_check_payload")
blocked_check_id=$(printf '%s' "$blocked_check" | jq -er '.data.id')
blocked_decision=$(curl -sS -o /dev/null -w '%{http_code}' -X POST "$backend_url/api/checks/$blocked_check_id/transition" \
  -H "Authorization: Bearer $reviewer_token" -H 'Content-Type: application/json' \
  -d '{"status":"pass","expectedVersion":1,"reason":"invalid snapshot must block the decision"}')
[ "$blocked_decision" = "422" ]

# 许可恢复后发运成功，快照版本递增且失效原因清空。
generator=$(curl -fsS "$backend_url/api/generators?search=WG-001" -H "Authorization: Bearer $reviewer_token")
generator_id=$(printf '%s' "$generator" | jq -er '.data[0].id')
generator_version=$(printf '%s' "$generator" | jq -er '.data[0].version')
curl -fsS -X POST "$backend_url/api/generators/$generator_id/transition" -H "Authorization: Bearer $reviewer_token" \
  -H 'Content-Type: application/json' -d "{\"status\":\"active\",\"expectedVersion\":$generator_version,\"reason\":\"validation reinstates the permit\"}" >/dev/null
dispatched=$(curl -fsS -X POST "$backend_url/api/manifests/$manifest_id/transition" \
  -H "Authorization: Bearer $operator_token" -H 'X-Request-ID: validation-manifest-dispatch' -H 'Content-Type: application/json' \
  -d "{\"status\":\"in_transit\",\"expectedVersion\":$manifest_version,\"reason\":\"permits verified again before dispatch\"}")
printf '%s' "$dispatched" | jq -e '.data.status == "in_transit" and .data.snapshotVersion == 2 and .data.snapshotInvalidReason == ""' >/dev/null

stale_status=$(curl -sS -o /dev/null -w '%{http_code}' -X POST "$backend_url/api/manifests/$manifest_id/transition" \
  -H "Authorization: Bearer $operator_token" -H 'Content-Type: application/json' \
  -d '{"status":"received","expectedVersion":1,"reason":"stale version must conflict"}')
[ "$stale_status" = "409" ]

blocked_code="TM-BLOCKED-$stamp"
blocked_payload=$(printf '%s' "$manifest_payload" | jq --arg code "$blocked_code" '.code=$code | .carrierCode="CP-001"')
blocked=$(curl -fsS -X POST "$backend_url/api/manifests" -H "Authorization: Bearer $operator_token" -H 'Content-Type: application/json' -d "$blocked_payload")
blocked_id=$(printf '%s' "$blocked" | jq -er '.data.id')
blocked_status=$(curl -sS -o /dev/null -w '%{http_code}' -X POST "$backend_url/api/manifests/$blocked_id/transition" \
  -H "Authorization: Bearer $operator_token" -H 'Content-Type: application/json' \
  -d '{"status":"submitted","expectedVersion":1,"reason":"unverified carrier must block"}')
[ "$blocked_status" = "422" ]

check_code="CC-VALIDATE-$stamp"
check_payload=$(jq -nc --arg code "$check_code" --arg manifest "$manifest_code" --arg now "$now" '{
  code:$code,name:"空卷验收核验",description:"Compose compliance decision validation",manifestCode:$manifest,
  checklist:"产废许可、承运资质、联单数量、处置去向",decisionBasis:"",facility:"复核中心",owner:"reviewer",
  category:"联单复核",riskLevel:"medium",metricValue:92,metricUnit:"score",effectiveAt:$now,
  evidence:"minio://evidence/validation/check.pdf",relatedCode:$manifest
}')
check=$(curl -fsS -X POST "$backend_url/api/checks" -H "Authorization: Bearer $operator_token" \
  -H 'X-Request-ID: validation-check-create' -H 'Content-Type: application/json' -d "$check_payload")
check_id=$(printf '%s' "$check" | jq -er '.data.id')
check_version=$(printf '%s' "$check" | jq -er '.data.version')

operator_decision=$(curl -sS -o /dev/null -w '%{http_code}' -X POST "$backend_url/api/checks/$check_id/transition" \
  -H "Authorization: Bearer $operator_token" -H 'Content-Type: application/json' \
  -d "{\"status\":\"pass\",\"expectedVersion\":$check_version,\"reason\":\"operator cannot decide\"}")
[ "$operator_decision" = "403" ]

decision=$(curl -fsS -X POST "$backend_url/api/checks/$check_id/transition" \
  -H "Authorization: Bearer $reviewer_token" -H 'X-Request-ID: validation-reviewer-decision' -H 'Content-Type: application/json' \
  -d "{\"status\":\"pass\",\"expectedVersion\":$check_version,\"reason\":\"all evidence groups verified\"}")
printf '%s' "$decision" | jq -e '.data.status == "pass" and .data.version == 2 and .data.decisionBasis == "all evidence groups verified"' >/dev/null

viewer_audit_status=$(curl -sS -o /dev/null -w '%{http_code}' "$backend_url/api/audits" -H "Authorization: Bearer $viewer_token")
[ "$viewer_audit_status" = "403" ]
audits=$(curl -fsS "$backend_url/api/audits?page=1&pageSize=100" -H "Authorization: Bearer $reviewer_token")
printf '%s' "$audits" | jq -e '([.data[].requestId]) as $ids | ($ids | index("validation-manifest-submit")) != null and ($ids | index("validation-reviewer-decision")) != null and ($ids | index("validation-dispatch-blocked")) != null' >/dev/null
curl -fsS "$backend_url/api/audit-summary?windowHours=24" -H "Authorization: Bearer $reviewer_token" | jq -e '.data.total >= 5 and .data.transitions >= 2' >/dev/null

docker compose ps
[ "${KEEP_RUNNING:-0}" = "1" ] && echo "KEEP_RUNNING=1: containers left running for built-in Browser validation"
