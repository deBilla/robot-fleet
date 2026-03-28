#!/bin/bash
# Submit telemetry analytics job to Spark cluster
#
# Usage:
#   docker exec robot-fleet-spark-master-1 /opt/spark-jobs/run.sh
#   docker exec robot-fleet-spark-master-1 /opt/spark-jobs/run.sh --date 2026/03/28

#!/bin/bash
# Run telemetry analytics — local mode for dev, cluster mode for prod
# Usage:
#   docker exec robot-fleet-spark-master-1 /opt/spark-jobs/run.sh
#   docker exec robot-fleet-spark-master-1 /opt/spark-jobs/run.sh --date 2026/03/28

export SPARK_HOME=/opt/spark
mkdir -p /tmp/ivy2/cache /tmp/ivy2/jars 2>/dev/null

/opt/spark/bin/spark-submit \
  --master local[*] \
  --packages org.apache.hadoop:hadoop-aws:3.3.4,com.amazonaws:aws-java-sdk-bundle:1.12.262 \
  --conf spark.jars.ivy=/tmp/ivy2 \
  --conf spark.driver.memory=512m \
  --conf spark.hadoop.fs.s3a.endpoint=http://minio:9000 \
  --conf spark.hadoop.fs.s3a.access.key=fleetos \
  --conf spark.hadoop.fs.s3a.secret.key=fleetos123 \
  --conf spark.hadoop.fs.s3a.path.style.access=true \
  --conf spark.hadoop.fs.s3a.impl=org.apache.hadoop.fs.s3a.S3AFileSystem \
  --conf spark.hadoop.fs.s3a.connection.ssl.enabled=false \
  /opt/spark-jobs/telemetry_analytics.py --local "$@"
