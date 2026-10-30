import { InputAdornment, MenuItem } from "@mui/material";
import { useEffect, useRef, useState } from "react";
import { ByteSizeUnit, byteSizeUnitFactor, byteSizeUnits, splitBytes, toBytes, trimNumber } from "../../util/byteSize.ts";
import { DenseFilledTextField } from "./StyledComponents.tsx";
import { StyleOutlinedSelect } from "./SizeInput.tsx";

export interface ByteSizeInputProps {
  // Byte count, the value that gets stored.
  bytes: number;
  onChange: (bytes: number) => void;
  // Units offered by the dropdown; defaults to TB/GB/MB/KB.
  units?: ByteSizeUnit[];
  disabled?: boolean;
  required?: boolean;
  label?: string;
}

// ByteSizeInput edits a stored byte count as "amount + unit", the way capacity
// and traffic packs have always been described. The split on load picks the
// largest unit that divides the stored value exactly, so existing rows are
// displayed without changing what they mean.
export default function ByteSizeInput({
  bytes,
  onChange,
  units = byteSizeUnits,
  disabled,
  required,
  label,
}: ByteSizeInputProps) {
  const initial = splitBytes(bytes);
  const [unit, setUnit] = useState<ByteSizeUnit>(units.includes(initial.unit) ? initial.unit : units[0]);
  const [amount, setAmount] = useState<number>(initial.value);
  // Byte count this input last emitted, so an external change can be told
  // apart from the echo of our own onChange.
  const emitted = useRef<number | undefined>(undefined);

  useEffect(() => {
    if (emitted.current === bytes) {
      return;
    }
    const split = splitBytes(bytes);
    setUnit(units.includes(split.unit) ? split.unit : units[0]);
    setAmount(trimNumber(bytes / byteSizeUnitFactor(units.includes(split.unit) ? split.unit : units[0])));
  }, [bytes]);

  const emit = (value: number, nextUnit: ByteSizeUnit) => {
    setAmount(value);
    setUnit(nextUnit);
    const next = toBytes(value, nextUnit);
    emitted.current = next;
    onChange(next);
  };

  return (
    <DenseFilledTextField
      fullWidth
      type="number"
      disabled={disabled}
      required={required}
      label={label}
      value={Number.isFinite(amount) ? amount : 0}
      slotProps={{ htmlInput: { step: "any", min: 0 } }}
      onChange={(e) => emit(Math.max(0, Number(e.target.value) || 0), unit)}
      InputProps={{
        endAdornment: (
          <InputAdornment position="end">
            <StyleOutlinedSelect
              variant="filled"
              size="small"
              disabled={disabled}
              value={unit}
              onChange={(e) => emit(amount, e.target.value as ByteSizeUnit)}
            >
              {units.map((u) => (
                <MenuItem dense key={u} value={u}>
                  {u}
                </MenuItem>
              ))}
            </StyleOutlinedSelect>
          </InputAdornment>
        ),
      }}
    />
  );
}
