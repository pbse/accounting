// Whole percentages that add up to 100, the largest remainders rounded up.
export function percents(values: number[]) {
  const total = values.reduce((sum, v) => sum + v, 0);
  if (total === 0) return values.map(() => 0);
  const exact = values.map((v) => (100 * v) / total);
  const out = exact.map(Math.floor);
  const short = 100 - out.reduce((sum, v) => sum + v, 0);
  exact
    .map((v, i) => [v - Math.floor(v), i])
    .sort((a, b) => b[0] - a[0])
    .slice(0, short)
    .forEach(([, i]) => (out[i] += 1));
  return out;
}
