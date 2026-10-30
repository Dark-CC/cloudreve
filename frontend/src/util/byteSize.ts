// Binary size units used by every capacity input in the admin panel —
// storage packs, traffic packs and gift codes all store bytes, so the picker
// here is the single source of truth for the multiplier.

export type ByteSizeUnit = "KB" | "MB" | "GB" | "TB";

// Pickable units, largest first: capacity packs are normally sold in GB/TB.
export const byteSizeUnits: ByteSizeUnit[] = ["TB", "GB", "MB", "KB"];

export const byteSizeUnitFactor = (unit: ByteSizeUnit): number => {
  switch (unit) {
    case "KB":
      return 1024;
    case "MB":
      return 1024 * 1024;
    case "GB":
      return 1024 * 1024 * 1024;
    case "TB":
      return 1024 * 1024 * 1024 * 1024;
  }
};

// Largest unit that divides bytes exactly, so an existing 2048-byte pack
// shows as "2 KB" instead of "0.00 GB".
export const bestByteSizeUnit = (bytes: number): ByteSizeUnit => {
  for (const unit of byteSizeUnits) {
    const factor = byteSizeUnitFactor(unit);
    if (bytes >= factor && bytes % factor === 0) {
      return unit;
    }
  }
  return "KB";
};

export interface ByteSizeValue {
  // Amount in the selected unit; may be fractional (0.5 TB).
  value: number;
  unit: ByteSizeUnit;
}

// toBytes converts a value in the given unit into the stored byte count,
// rounding to a whole byte.
export const toBytes = (value: number, unit: ByteSizeUnit): number =>
  Math.round(value * byteSizeUnitFactor(unit));

// splitBytes is the inverse of toBytes: it picks the largest unit the byte
// count divides exactly into and returns the amount in that unit.
export const splitBytes = (bytes: number): ByteSizeValue => {
  if (!bytes || bytes <= 0) {
    return { value: 0, unit: "GB" };
  }
  const unit = bestByteSizeUnit(bytes);
  return { value: bytes / byteSizeUnitFactor(unit), unit };
};

// Round to at most `decimals` places without trailing zeros, so 1.50 reads
// as 1.5 and 0.01 stays 0.01.
export const trimNumber = (value: number, decimals = 4): number => {
  if (!Number.isFinite(value)) {
    return 0;
  }
  return Number(value.toFixed(decimals));
};
