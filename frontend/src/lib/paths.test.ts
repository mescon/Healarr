import { describe, it, expect } from 'vitest';
import { isAbsolutePath } from './paths';

describe('isAbsolutePath', () => {
  describe('POSIX absolute paths', () => {
    it.each([
      '/',
      '/media',
      '/media/Movies',
      '/mnt/storage/tv',
      '/foo/bar/baz.mkv',
    ])('accepts %s', (path) => {
      expect(isAbsolutePath(path)).toBe(true);
    });

    it('accepts // (also valid as a Linux-style UNC)', () => {
      expect(isAbsolutePath('//alexpr4100/media/TV Shows')).toBe(true);
    });
  });

  describe('Windows drive-letter paths', () => {
    it.each([
      'C:\\',
      'C:\\Media',
      'C:\\Media\\Movies',
      'D:\\Media\\TV Shows',
      'Z:\\foo',
    ])('accepts backslash form %s', (path) => {
      expect(isAbsolutePath(path)).toBe(true);
    });

    it.each(['C:/', 'C:/Media', 'd:/media/movies'])(
      'accepts forward-slash form %s',
      (path) => {
        expect(isAbsolutePath(path)).toBe(true);
      },
    );

    it('accepts lowercase drive letters', () => {
      expect(isAbsolutePath('c:\\foo')).toBe(true);
    });
  });

  describe('Windows UNC paths', () => {
    it.each([
      '\\\\server\\share',
      '\\\\server\\share\\foo',
      '\\\\alexpr4100\\media\\Movies',
    ])('accepts backslash UNC %s', (path) => {
      expect(isAbsolutePath(path)).toBe(true);
    });
  });

  describe('rejected paths', () => {
    it.each([
      '',
      'relative/path',
      'media',
      './foo',
      '../foo',
      'C:',
      'C:foo',
      '1:\\foo',
      '\\notUNC',
    ])('rejects %s', (path) => {
      expect(isAbsolutePath(path)).toBe(false);
    });
  });
});
