import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { useModalA11y, getFocusableElements } from './useModalA11y';

function Dialog({ isOpen, onClose }: { isOpen: boolean; onClose: () => void }) {
    const ref = useModalA11y(isOpen, onClose);
    if (!isOpen) return null;
    return (
        <div ref={ref} tabIndex={-1} role="dialog" aria-label="test dialog">
            <button>first</button>
            <button>middle</button>
            <button>last</button>
        </div>
    );
}

/** Append a child to `root` with optional attributes, using safe DOM methods. */
function append(root: HTMLElement, tag: string, attrs: Record<string, string> = {}) {
    const el = document.createElement(tag);
    for (const [k, v] of Object.entries(attrs)) el.setAttribute(k, v);
    root.appendChild(el);
    return el;
}

describe('getFocusableElements', () => {
    it('returns focusable descendants and skips disabled/[-1]', () => {
        const root = document.createElement('div');
        append(root, 'button');                       // focusable
        append(root, 'button', { disabled: '' });     // excluded (disabled)
        append(root, 'a', { href: '#x' });            // focusable
        append(root, 'input');                        // focusable
        append(root, 'div', { tabindex: '-1' });      // excluded (tabindex=-1)
        append(root, 'div', { tabindex: '0' });       // focusable

        const tags = getFocusableElements(root).map((el) => el.tagName.toLowerCase());
        expect(tags).toEqual(['button', 'a', 'input', 'div']);
    });
});

describe('useModalA11y', () => {
    beforeEach(() => {
        document.body.innerHTML = '';
    });

    it('moves focus to the first focusable element on open', () => {
        render(<Dialog isOpen onClose={() => {}} />);
        expect(document.activeElement).toBe(screen.getByText('first'));
    });

    it('closes on Escape', async () => {
        const user = userEvent.setup();
        const onClose = vi.fn();
        render(<Dialog isOpen onClose={onClose} />);
        await user.keyboard('{Escape}');
        expect(onClose).toHaveBeenCalledTimes(1);
    });

    it('traps Tab: wraps from last back to first', async () => {
        const user = userEvent.setup();
        render(<Dialog isOpen onClose={() => {}} />);
        screen.getByText('last').focus();
        await user.tab();
        expect(document.activeElement).toBe(screen.getByText('first'));
    });

    it('traps Shift+Tab: wraps from first back to last', async () => {
        const user = userEvent.setup();
        render(<Dialog isOpen onClose={() => {}} />);
        screen.getByText('first').focus();
        await user.tab({ shift: true });
        expect(document.activeElement).toBe(screen.getByText('last'));
    });

    it('restores focus to the previously focused element on close', () => {
        const trigger = document.createElement('button');
        trigger.textContent = 'opener';
        document.body.appendChild(trigger);
        trigger.focus();
        expect(document.activeElement).toBe(trigger);

        const { rerender } = render(<Dialog isOpen onClose={() => {}} />);
        expect(document.activeElement).toBe(screen.getByText('first'));

        rerender(<Dialog isOpen={false} onClose={() => {}} />);
        expect(document.activeElement).toBe(trigger);
    });

    it('does nothing while closed', async () => {
        const user = userEvent.setup();
        const onClose = vi.fn();
        render(<Dialog isOpen={false} onClose={onClose} />);
        await user.keyboard('{Escape}');
        expect(onClose).not.toHaveBeenCalled();
    });
});
