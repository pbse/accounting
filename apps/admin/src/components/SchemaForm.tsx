import { useEffect, useState } from "react";

// A form for the JSON Schema subset wf.Schema produces: an object of strings, numbers, integers and
// booleans becomes fields; anything nested is edited as JSON. Required fields are marked.
type Schema = { type?: string; properties?: Record<string, Schema>; required?: string[]; description?: string; sensitive?: boolean; format?: string };

export function SchemaForm({ schema, onChange }: { schema?: Schema; onChange: (value: unknown, valid: boolean) => void }) {
  const properties = schema?.properties;
  const [values, setValues] = useState<Record<string, string | boolean>>({});
  const [raw, setRaw] = useState("{}");
  const build = (next: Record<string, string | boolean>): [unknown, boolean] => {
    if (!properties) return [{}, true];
    const out: Record<string, unknown> = {};
    let valid = true;
    for (const [key, property] of Object.entries(properties)) {
      const v = next[key];
      if (v === undefined || v === "") {
        if (schema?.required?.includes(key) && property.type !== "boolean") valid = false;
        if (property.type === "boolean") out[key] = false;
        continue;
      }
      if (property.type === "integer" || property.type === "number") {
        const n = Number(v);
        if (Number.isNaN(n) || (property.type === "integer" && !Number.isInteger(n))) valid = false;
        out[key] = n;
      } else if (property.type === "boolean") {
        out[key] = v === true;
      } else if (property.type === "string") {
        out[key] = v;
      } else {
        try {
          out[key] = JSON.parse(String(v));
        } catch {
          valid = false;
        }
      }
    }
    return [out, valid];
  };
  // The untouched form counts too: required fields start empty, so it starts invalid.
  useEffect(() => {
    onChange(...build({}));
  }, []);
  if (!properties) {
    return (
      <label>
        Input (JSON)
        <textarea rows={6} value={raw} onChange={(event) => {
          setRaw(event.target.value);
          try {
            onChange(JSON.parse(event.target.value), true);
          } catch {
            onChange(undefined, false);
          }
        }} />
      </label>
    );
  }
  const update = (name: string, value: string | boolean) => {
    const next = { ...values, [name]: value };
    setValues(next);
    onChange(...build(next));
  };
  return (
    <fieldset className="schema-form">
      <legend>Input</legend>
      {Object.entries(properties).map(([name, property]) => {
        const required = schema?.required?.includes(name);
        const label = (
          <span>
            {name.replaceAll("_", " ")}
            {required && property.type !== "boolean" && <abbr title="required"> *</abbr>}
            {property.sensitive && <small> (client content)</small>}
          </span>
        );
        if (property.type === "boolean") {
          return (
            <label key={name} className="check">
              <input type="checkbox" checked={values[name] === true} onChange={(event) => update(name, event.target.checked)} />
              {label}
            </label>
          );
        }
        const simple = property.type === "string" || property.type === "integer" || property.type === "number";
        return (
          <label key={name}>
            {label}
            {simple ? (
              <input
                type={property.type === "string" ? (property.sensitive ? "password" : "text") : "number"}
                value={String(values[name] ?? "")}
                onChange={(event) => update(name, event.target.value)}
                required={required}
                autoComplete="off"
              />
            ) : (
              <textarea rows={3} placeholder="JSON" required={required} value={String(values[name] ?? "")} onChange={(event) => update(name, event.target.value)} />
            )}
            {property.description && <small>{property.description}</small>}
          </label>
        );
      })}
    </fieldset>
  );
}
