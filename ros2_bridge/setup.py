from setuptools import setup

package_name = 'fleetos_bridge'

setup(
    name=package_name,
    version='0.1.0',
    packages=[package_name],
    install_requires=['setuptools', 'redis', 'requests'],
    zip_safe=True,
    maintainer='FleetOS',
    maintainer_email='dev@fleetos.dev',
    description='FleetOS ROS 2 Bridge',
    license='MIT',
    entry_points={
        'console_scripts': [
            'bridge_node = fleetos_bridge.bridge_node:main',
        ],
    },
    data_files=[
        ('share/ament_index/resource_index/packages', ['resource/fleetos_bridge']),
        ('share/' + package_name, ['package.xml']),
        ('share/' + package_name + '/launch', ['launch/fleet_bridge.launch.py']),
    ],
)
