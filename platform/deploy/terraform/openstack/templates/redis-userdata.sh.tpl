#!/bin/bash
set -euo pipefail

# Install Redis 7
apt-get update -y
apt-get install -y redis-server

# Tuning
cat > /etc/redis/redis.conf <<CONF
bind 0.0.0.0
port 6379
protected-mode no
maxmemory 2gb
maxmemory-policy allkeys-lru
save 900 1
save 300 10
save 60 10000
appendonly yes
appendfsync everysec
CONF

%{ if total_nodes > 1 && node_index > 0 ~}
# Replica configuration — node 0 is primary
echo "replicaof ${cluster_name}-redis-0 6379" >> /etc/redis/redis.conf
%{ endif ~}

systemctl enable redis-server
systemctl restart redis-server
