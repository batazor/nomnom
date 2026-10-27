// Projection between latitude/longitude and the Virtual City grid
// (FR-658), the same one delivery uses: x blocks east and y blocks north of
// the south-west corner. The map draws by it and the mocks place points by
// it, so both stay in step with delivery.

export type GeoPoint = Readonly<{ latitude: number; longitude: number }>;

export type VirtualCity = Readonly<{
  origin: GeoPoint;
  blocks: number;
  blockMeters: number;
}>;

// A point of the grid; fractions lie between intersections.
export type GridPoint = Readonly<{ x: number; y: number }>;

// Meters of one degree of latitude; one degree of longitude is this times
// cos(latitude of the corner).
const metersPerDegree = 111_320;

function longitudeMeters(city: VirtualCity): number {
  return metersPerDegree * Math.cos((city.origin.latitude * Math.PI) / 180);
}

export function toGrid(city: VirtualCity, point: GeoPoint): GridPoint {
  return {
    x:
      ((point.longitude - city.origin.longitude) * longitudeMeters(city)) /
      city.blockMeters,
    y:
      ((point.latitude - city.origin.latitude) * metersPerDegree) /
      city.blockMeters,
  };
}

export function fromGrid(city: VirtualCity, point: GridPoint): GeoPoint {
  return {
    latitude:
      city.origin.latitude + (point.y * city.blockMeters) / metersPerDegree,
    longitude:
      city.origin.longitude +
      (point.x * city.blockMeters) / longitudeMeters(city),
  };
}
