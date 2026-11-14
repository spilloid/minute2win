# Embedded in each generated script. Reporting never gates a deployment.
report() {
    payload=$(printf '{"macs":["%s"],"key":"%s","phase":"%s","status":"%s","message":"%s"}' \
        "$M2W_MAC" "$M2W_KEY" "$1" "$2" "$3")
    if command -v curl >/dev/null 2>&1; then
        curl -fsS --max-time 10 -H 'Content-Type: application/json' --data "$payload" "$M2W_REPORT" >/dev/null 2>&1 || true
    else
        wget -q -T 10 -O /dev/null --header='Content-Type: application/json' --post-data="$payload" "$M2W_REPORT" >/dev/null 2>&1 || true
    fi
}
