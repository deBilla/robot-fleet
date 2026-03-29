FROM ros:humble-ros-base

# Install Python deps
RUN apt-get update && apt-get install -y --no-install-recommends \
    python3-pip \
    && rm -rf /var/lib/apt/lists/*

RUN pip3 install --no-cache-dir redis requests

# Copy package
WORKDIR /ros2_ws/src/fleetos_bridge
COPY package.xml setup.py setup.cfg ./
COPY resource/ resource/
COPY launch/ launch/
COPY fleetos_bridge/ fleetos_bridge/

# Build
WORKDIR /ros2_ws
RUN . /opt/ros/humble/setup.sh && \
    colcon build --packages-select fleetos_bridge --symlink-install

# Source workspace on entry
COPY <<'EOF' /ros_entrypoint.sh
#!/bin/bash
set -e
source /opt/ros/humble/setup.bash
source /ros2_ws/install/setup.bash
exec "$@"
EOF
RUN chmod +x /ros_entrypoint.sh

ENTRYPOINT ["/ros_entrypoint.sh"]
CMD ["ros2", "run", "fleetos_bridge", "bridge_node"]
