const allowedTopLevelNames = new Set([".DS_Store", "README.md", "probe", "testdata", "validate"]);

export function isAllowedTopLevelEntry(name) {
  return allowedTopLevelNames.has(name) || /\.(go|mod|sum)$/u.test(name);
}
