package proxy

import "fmt"

// ProxyInitScript installs CA cert, sets up socat bridge + HTTP_PROXY.
func ProxyInitScript(proxyPort int) string {
	if proxyPort == 0 {
		return ""
	}
	return fmt.Sprintf(`sudo cp /tmp/proxy-ca.crt /usr/local/share/ca-certificates/agent-vm-proxy-ca.crt && sudo update-ca-certificates 2>/dev/null
GW=$(ip route show default 2>/dev/null | awk '{print $3}' | head -1)
if [ -z "$GW" ]; then
    HEX=$(awk '$2=="00000000" {print $3; exit}' /proc/net/route 2>/dev/null)
    if [ -n "$HEX" ] && [ ${#HEX} -eq 8 ]; then
        GW=$(printf "%%d.%%d.%%d.%%d" 0x${HEX:6:2} 0x${HEX:4:2} 0x${HEX:2:2} 0x${HEX:0:2})
    fi
fi
if [ -n "$GW" ]; then
    socat TCP-LISTEN:18080,fork,reuseaddr,bind=127.0.0.1 TCP:$GW:%d >/tmp/proxy-bridge.log 2>&1 &
    printf '\nexport HTTP_PROXY=http://127.0.0.1:18080\nexport HTTPS_PROXY=http://127.0.0.1:18080\nexport NO_PROXY=localhost,127.0.0.1\n' >> ~/.dev-tools.sh
    echo "[agent-vm] proxy bridge: 127.0.0.1:18080 -> $GW:%d (MITM enabled)"
else
    echo "[agent-vm] WARNING: could not detect gateway, proxy not bridged"
fi`, proxyPort, proxyPort)
}
