/**
 * Static lab room geometry matching playground/simulator/assets/lab_room.xml.
 *
 * Coordinate mapping: MuJoCo Z-up -> Three.js Y-up
 *   Three.js x = MuJoCo x
 *   Three.js y = MuJoCo z
 *   Three.js z = -MuJoCo y
 */

function m2t(mx: number, my: number, mz: number): [number, number, number] {
  return [mx, mz, -my];
}

export function LabRoom() {
  return (
    <group>
      {/* Floor - 10x10m */}
      <mesh rotation={[-Math.PI / 2, 0, 0]} position={[0, 0, 0]} receiveShadow>
        <planeGeometry args={[10, 10]} />
        <meshStandardMaterial color="#1a1a1e" />
      </mesh>
      {/* Floor grid lines */}
      <gridHelper args={[10, 20, '#2a2a30', '#222228']} position={[0, 0.001, 0]} />

      {/* Walls - 3m tall */}
      <Wall position={m2t(0, 5, 1.5)} size={[10, 3, 0.1]} />
      <Wall position={m2t(0, -5, 1.5)} size={[10, 3, 0.1]} />
      <Wall position={m2t(5, 0, 1.5)} size={[0.1, 3, 10]} />
      <Wall position={m2t(-5, 0, 1.5)} size={[0.1, 3, 10]} />

      {/* Workbench at MuJoCo (3, 3, 0) */}
      <group position={m2t(3, 3, 0)}>
        {/* Tabletop */}
        <mesh position={m2t(0, 0, 0.75)} castShadow>
          <boxGeometry args={[1.2, 0.04, 0.8]} />
          <meshStandardMaterial color="#5c4a3a" />
        </mesh>
        {/* Legs */}
        {[[-0.5, -0.3], [0.5, -0.3], [-0.5, 0.3], [0.5, 0.3]].map(([lx, ly], i) => (
          <mesh key={`leg${i}`} position={m2t(lx, ly, 0.37)}>
            <cylinderGeometry args={[0.03, 0.03, 0.74, 8]} />
            <meshStandardMaterial color="#5c4a3a" />
          </mesh>
        ))}
        {/* Red box on table */}
        <mesh position={m2t(0.2, 0, 0.85)} castShadow>
          <boxGeometry args={[0.16, 0.16, 0.16]} />
          <meshStandardMaterial color="#b84030" />
        </mesh>
        {/* Blue cylinder on table */}
        <mesh position={m2t(-0.2, 0.1, 0.87)}>
          <cylinderGeometry args={[0.05, 0.05, 0.2, 16]} />
          <meshStandardMaterial color="#3070a0" />
        </mesh>
      </group>

      {/* Shelf at MuJoCo (-4.5, 0, 0) */}
      <group position={m2t(-4.5, 0, 0)}>
        {/* Back panel */}
        <mesh position={m2t(0, 0, 1.0)}>
          <boxGeometry args={[0.04, 2.0, 2.0]} />
          <meshStandardMaterial color="#5c4a3a" />
        </mesh>
        {/* Shelves */}
        {[0.4, 0.9, 1.4].map((h, i) => (
          <mesh key={`shelf${i}`} position={m2t(0.15, 0, h)}>
            <boxGeometry args={[0.6, 0.03, 2.0]} />
            <meshStandardMaterial color="#5c4a3a" />
          </mesh>
        ))}
      </group>

      {/* Floor markers */}
      <mesh position={[0, 0.002, 0]} rotation={[-Math.PI / 2, 0, 0]}>
        <circleGeometry args={[0.3, 32]} />
        <meshStandardMaterial color="#20c060" transparent opacity={0.3} />
      </mesh>
      <mesh position={m2t(2, -2, 0.002)} rotation={[-Math.PI / 2, 0, 0]}>
        <circleGeometry args={[0.3, 32]} />
        <meshStandardMaterial color="#20c060" transparent opacity={0.3} />
      </mesh>
    </group>
  );
}

function Wall({ position, size }: { position: [number, number, number]; size: [number, number, number] }) {
  return (
    <mesh position={position}>
      <boxGeometry args={size} />
      <meshStandardMaterial color="#2a2c30" />
    </mesh>
  );
}
