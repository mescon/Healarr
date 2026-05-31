/**
 * Returns true if `path` is an absolute path in any of the forms we accept:
 *   - POSIX absolute:           /foo, /foo/bar
 *   - Windows drive-letter:     C:\foo, c:/foo, D:\Media\Movies
 *   - Windows UNC (backslash):  \\server\share\foo
 *   - Windows UNC (forward):    //server/share/foo  (covered by the POSIX rule)
 *
 * Healarr can run on either Linux or Windows, and even when it runs on one
 * OS the *arr it talks to may run on the other. local_path must match
 * Healarr's host (the process opens the file); arr_path must match the
 * *arr's host (it is a label substituted into *arr API calls). Both fields
 * therefore need to accept absolute paths from either OS.
 */
export function isAbsolutePath(path: string): boolean {
  if (path.startsWith('/')) return true;
  if (path.startsWith('\\\\')) return true;
  return /^[A-Za-z]:[\\/]/.test(path);
}

export const absolutePathErrorMessage =
  'Must be an absolute path (Linux /path, Windows C:\\path, or UNC \\\\server\\share)';
