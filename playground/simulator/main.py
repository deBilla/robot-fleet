"""
MuJoCo lab room robot simulator for FleetOS.

Runs real physics simulation in a custom lab environment and streams
telemetry via gRPC to the ingestion service. Receives commands via gRPC
StreamCommands. Rendering is done client-side via Three.js — physics only.

Environment variables:
    ROBOT_ID        Initial robot identifier (default: robot-0001)
    GRPC_TARGET     Ingestion gRPC endpoint (default: ingestion:50051)
    SIM_STEP_HZ     Physics step rate (default: 50)
    TELEMETRY_HZ    Telemetry send rate (default: 10)
    CONTROL_PORT    HTTP control server port (default: 8085)
"""

import logging
import os
import queue
import signal
import sys
import time

logging.basicConfig(
    level=logging.INFO,
    format='{"time":"%(asctime)s","level":"%(levelname)s","msg":"%(message)s"}',
    datefmt="%Y-%m-%dT%H:%M:%S",
)
log = logging.getLogger(__name__)


def main():
    robot_id = os.environ.get("ROBOT_ID", "robot-0001")
    grpc_target = os.environ.get("GRPC_TARGET", "ingestion:50051")
    sim_step_hz = int(os.environ.get("SIM_STEP_HZ", "50"))
    telemetry_hz = int(os.environ.get("TELEMETRY_HZ", "10"))
    control_port = int(os.environ.get("CONTROL_PORT", "8085"))

    log.info("Starting MuJoCo lab simulator robot_id=%s target=%s", robot_id, grpc_target)
    log.info("Physics: %d Hz, Telemetry: %d Hz", sim_step_hz, telemetry_hz)

    from mujoco_env import LabSimulation
    from command_handler import CommandHandler
    from telemetry_client import TelemetryClient
    from frame_server import start_server, set_spawn_callback, set_list_robots_callback

    sim = LabSimulation()
    spawn_queue: queue.Queue[str] = queue.Queue()
    spawn_results: dict[str, dict] = {}

    # Per-robot command handlers — each opens a gRPC StreamCommands stream.
    def _make_handler(rid: str) -> CommandHandler:
        h = CommandHandler(grpc_target, rid)
        h.start()
        return h

    commands: dict[str, CommandHandler] = {robot_id: _make_handler(robot_id)}
    telemetry = TelemetryClient(grpc_target, robot_id)

    # Spawn runs on the main thread via queue (MuJoCo is not thread-safe)
    def request_spawn() -> dict:
        req_id = str(time.time())
        spawn_queue.put(req_id)
        for _ in range(20):
            time.sleep(0.1)
            if req_id in spawn_results:
                return spawn_results.pop(req_id)
        return {"error": "spawn timeout"}

    set_spawn_callback(request_spawn)
    set_list_robots_callback(sim.get_all_robot_ids)
    start_server(control_port)

    # Connect to ingestion gRPC
    connected = False
    for attempt in range(30):
        try:
            telemetry.connect()
            connected = True
            break
        except Exception as e:
            log.warning("gRPC connect attempt %d failed: %s", attempt + 1, e)
            time.sleep(2)

    if not connected:
        log.error("Failed to connect to ingestion after 30 attempts")
        sys.exit(1)

    # Timing
    step_interval = 1.0 / sim_step_hz
    telemetry_every = max(1, sim_step_hz // telemetry_hz)
    lidar_every = sim_step_hz  # 1 Hz

    # Graceful shutdown
    running = True

    def shutdown(signum, frame):
        nonlocal running
        log.info("Shutting down (signal %d)", signum)
        running = False

    signal.signal(signal.SIGTERM, shutdown)
    signal.signal(signal.SIGINT, shutdown)

    log.info("Simulation loop started")
    step = 0

    try:
        while running:
            loop_start = time.monotonic()

            # Process spawn requests from HTTP thread
            while not spawn_queue.empty():
                req_id = spawn_queue.get_nowait()
                result = sim.spawn_robot()
                spawn_results[req_id] = result
                new_rid = result.get("robot_id")
                if new_rid and new_rid not in commands:
                    commands[new_rid] = _make_handler(new_rid)

            # Get actions for each robot from its own command handler.
            robot_ids = sim.get_all_robot_ids()
            sim_time = sim.sim_time
            actions = {}
            for rid in robot_ids:
                if rid not in commands:
                    commands[rid] = _make_handler(rid)
                actions[rid] = commands[rid].get_action(sim_time)

            # Step physics
            sim.step(actions)
            step += 1

            # Send telemetry for all robots
            if step % telemetry_every == 0:
                for rid in robot_ids:
                    state = sim.get_robot_state(rid)
                    if state:
                        telemetry.send_state(state, robot_id=rid)

            # Send LiDAR at 1 Hz for all robots
            if step % lidar_every == 0:
                for rid in robot_ids:
                    telemetry.send_lidar(robot_id=rid)

            # Log progress
            if step % (sim_step_hz * 10) == 0:
                state = sim.get_robot_state(robot_ids[0]) if robot_ids else None
                if state:
                    first_cmd = commands[robot_ids[0]].current_command if robot_ids else "idle"
                    log.info("step=%d robots=%d pos=(%.2f, %.2f, %.2f) bat=%.0f%% cmd=%s",
                             step, len(robot_ids),
                             state["pos_x"], state["pos_y"], state["pos_z"],
                             state["battery"] * 100, first_cmd)

            # Maintain step rate
            elapsed = time.monotonic() - loop_start
            sleep_time = step_interval - elapsed
            if sleep_time > 0:
                time.sleep(sleep_time)

    except KeyboardInterrupt:
        log.info("Interrupted")
    finally:
        log.info("Cleaning up after %d steps", step)
        sim.close()
        for h in commands.values():
            h.close()
        telemetry.close()


if __name__ == "__main__":
    main()
