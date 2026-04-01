#!/bin/bash
set -euo pipefail

BROKER_ID=${broker_id}
BROKER_COUNT=${broker_count}

# Install Java (Kafka dependency)
apt-get update -y
apt-get install -y openjdk-17-jre-headless wget

# Format and mount data volume
mkfs.ext4 -F /dev/vdb || true
mkdir -p /var/kafka-data
mount /dev/vdb /var/kafka-data
echo "/dev/vdb /var/kafka-data ext4 defaults,nofail 0 2" >> /etc/fstab

# Download and install Kafka
KAFKA_VERSION="3.7.0"
SCALA_VERSION="2.13"
wget -q "https://downloads.apache.org/kafka/$${KAFKA_VERSION}/kafka_$${SCALA_VERSION}-$${KAFKA_VERSION}.tgz" -O /tmp/kafka.tgz
mkdir -p /opt/kafka
tar -xzf /tmp/kafka.tgz -C /opt/kafka --strip-components=1

# Create kafka user
useradd -r -s /bin/false kafka
chown -R kafka:kafka /opt/kafka /var/kafka-data

# Build broker list for controller quorum
CONTROLLERS=""
for i in $(seq 0 $(($${BROKER_COUNT} - 1))); do
  if [ -n "$${CONTROLLERS}" ]; then
    CONTROLLERS="$${CONTROLLERS},"
  fi
  CONTROLLERS="$${CONTROLLERS}$${i}@kafka-$${i}.fleetos.internal:9093"
done

# KRaft configuration (no ZooKeeper)
cat > /opt/kafka/config/kraft/server.properties <<CONF
process.roles=broker,controller
node.id=$${BROKER_ID}
controller.quorum.voters=$${CONTROLLERS}
listeners=PLAINTEXT://:9092,CONTROLLER://:9093
inter.broker.listener.name=PLAINTEXT
advertised.listeners=PLAINTEXT://kafka-$${BROKER_ID}.fleetos.internal:9092
controller.listener.names=CONTROLLER
log.dirs=/var/kafka-data/kraft-logs
num.partitions=12
default.replication.factor=$${BROKER_COUNT}
min.insync.replicas=$(($${BROKER_COUNT} > 1 ? 2 : 1))
log.retention.hours=168
log.segment.bytes=1073741824
auto.create.topics.enable=false
CONF

# Format storage
CLUSTER_ID=$(/opt/kafka/bin/kafka-storage.sh random-uuid)
/opt/kafka/bin/kafka-storage.sh format -t "$${CLUSTER_ID}" -c /opt/kafka/config/kraft/server.properties

# Systemd service
cat > /etc/systemd/system/kafka.service <<SERVICE
[Unit]
Description=Apache Kafka (KRaft)
After=network.target

[Service]
Type=simple
User=kafka
ExecStart=/opt/kafka/bin/kafka-server-start.sh /opt/kafka/config/kraft/server.properties
ExecStop=/opt/kafka/bin/kafka-server-stop.sh
Restart=on-failure
RestartSec=10
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
SERVICE

systemctl daemon-reload
systemctl enable kafka
systemctl start kafka
