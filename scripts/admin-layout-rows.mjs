// A completed sweep must account for every requested route, even if its
// navigation loop is accidentally changed to skip the back office.
export async function measureAdminRows(rows, measure) {
  const required = rows.map(row => row.label);
  const visited = [];
  for (const row of rows) {
    await measure(row);
    visited.push(row.label);
  }
  if (required.length === 0 || required.some(label => !visited.includes(label))) {
    throw new Error('admin coverage missing required rows');
  }
}
