from launch import LaunchDescription
from launch_ros.actions import Node
from launch.actions import DeclareLaunchArgument
from launch.substitutions import LaunchConfiguration


def generate_launch_description():
    return LaunchDescription([
        DeclareLaunchArgument('redis_host', default_value='redis'),
        DeclareLaunchArgument('redis_port', default_value='6379'),
        DeclareLaunchArgument('api_url', default_value='http://api:8080'),
        DeclareLaunchArgument('api_key', default_value='dev-key-001'),
        DeclareLaunchArgument('robot_count', default_value='10'),

        Node(
            package='fleetos_bridge',
            executable='bridge_node',
            name='fleetos_bridge',
            output='screen',
            parameters=[{
                'redis_host': LaunchConfiguration('redis_host'),
                'redis_port': LaunchConfiguration('redis_port'),
                'api_url': LaunchConfiguration('api_url'),
                'api_key': LaunchConfiguration('api_key'),
                'robot_count': LaunchConfiguration('robot_count'),
            }],
        ),
    ])
