#!/bin/bash
set -euo pipefail

HARBOR_HOSTNAME="${harbor_hostname}"

# Install Docker + Docker Compose
apt-get update -y
apt-get install -y ca-certificates curl gnupg
install -m 0755 -d /etc/apt/keyrings
curl -fsSL https://download.docker.com/linux/ubuntu/gpg | gpg --dearmor -o /etc/apt/keyrings/docker.gpg
chmod a+r /etc/apt/keyrings/docker.gpg
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.gpg] https://download.docker.com/linux/ubuntu $(. /etc/os-release && echo "$VERSION_CODENAME") stable" > /etc/apt/sources.list.d/docker.list
apt-get update -y
apt-get install -y docker-ce docker-ce-cli containerd.io docker-compose-plugin

systemctl enable docker
systemctl start docker

# Format and mount data volume
mkfs.ext4 -F /dev/vdb || true
mkdir -p /data/harbor
mount /dev/vdb /data/harbor
echo "/dev/vdb /data/harbor ext4 defaults,nofail 0 2" >> /etc/fstab

# Download and install Harbor
HARBOR_VERSION="2.11.0"
curl -fsSL "https://github.com/goharbor/harbor/releases/download/v$${HARBOR_VERSION}/harbor-online-installer-v$${HARBOR_VERSION}.tgz" -o /tmp/harbor.tgz
tar -xzf /tmp/harbor.tgz -C /opt/

# Configure Harbor
cp /opt/harbor/harbor.yml.tmpl /opt/harbor/harbor.yml
sed -i "s|hostname: reg.mydomain.com|hostname: $${HARBOR_HOSTNAME}|" /opt/harbor/harbor.yml
sed -i "s|harbor_admin_password: Harbor12345|harbor_admin_password: CHANGE_ME_AT_DEPLOY_TIME|" /opt/harbor/harbor.yml
sed -i '/^https:/,/private_key:/s/^/#/' /opt/harbor/harbor.yml
sed -i "s|/your/certificate/path|/data/harbor/cert/server.crt|" /opt/harbor/harbor.yml
sed -i "s|data_volume: /data|data_volume: /data/harbor|" /opt/harbor/harbor.yml

# Install Harbor
cd /opt/harbor && ./install.sh --with-trivy
