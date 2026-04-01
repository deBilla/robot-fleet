#!/bin/bash
set -euo pipefail

# Install ClickHouse
apt-get update -y
apt-get install -y apt-transport-https ca-certificates curl gnupg
curl -fsSL https://packages.clickhouse.com/rpm/lts/repodata/repomd.xml.key | gpg --dearmor -o /usr/share/keyrings/clickhouse-keyring.gpg
echo "deb [signed-by=/usr/share/keyrings/clickhouse-keyring.gpg] https://packages.clickhouse.com/deb stable main" > /etc/apt/sources.list.d/clickhouse.list
apt-get update -y
DEBIAN_FRONTEND=noninteractive apt-get install -y clickhouse-server clickhouse-client

# Format and mount data volume
mkfs.ext4 -F /dev/vdb || true
mkdir -p /var/lib/clickhouse
mount /dev/vdb /var/lib/clickhouse
echo "/dev/vdb /var/lib/clickhouse ext4 defaults,nofail 0 2" >> /etc/fstab
chown -R clickhouse:clickhouse /var/lib/clickhouse

# Listen on all interfaces
sed -i 's|<listen_host>::1</listen_host>|<listen_host>0.0.0.0</listen_host>|' /etc/clickhouse-server/config.xml

# Enable Prometheus metrics endpoint
cat >> /etc/clickhouse-server/config.d/prometheus.xml <<PROM
<clickhouse>
    <prometheus>
        <endpoint>/metrics</endpoint>
        <port>9363</port>
        <metrics>true</metrics>
        <events>true</events>
        <asynchronous_metrics>true</asynchronous_metrics>
    </prometheus>
</clickhouse>
PROM

systemctl enable clickhouse-server
systemctl start clickhouse-server
