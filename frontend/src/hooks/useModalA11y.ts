import { useEffect, useRef } from 'react';

/**
 * All focusable descendants of `container`, in DOM order.
 */
export function getFocusableElements(container: HTMLElement): HTMLElement[] {
    return Array.from(
        container.querySelectorAll<HTMLElement>(
            'button:not([disabled]), [href], input:not([disabled]), select:not([disabled]), textarea:not([disabled]), [tabindex]:not([tabindex="-1"]):not([disabled])'
        )
    );
}

/**
 * Accessibility wiring shared by modal dialogs. While `isOpen` is true it:
 * - moves focus into the dialog (first focusable element, else the container)
 * - traps Tab / Shift+Tab within the dialog
 * - closes on Escape
 * - restores focus to the previously-focused element when it closes/unmounts
 *
 * Attach the returned ref to the dialog container and give that element
 * `tabIndex={-1}` so it can hold focus when it has no focusable children yet
 * (e.g. while loading).
 *
 * `onClose` is read through a ref, so passing a fresh inline callback each
 * render does not re-run the trap (which would clobber the saved focus target).
 */
export function useModalA11y<T extends HTMLElement = HTMLDivElement>(
    isOpen: boolean,
    onClose: () => void
) {
    const containerRef = useRef<T>(null);
    const onCloseRef = useRef(onClose);
    useEffect(() => {
        onCloseRef.current = onClose;
    });

    useEffect(() => {
        if (!isOpen) return;

        const previouslyFocused = document.activeElement as HTMLElement | null;
        const container = containerRef.current;

        // Move focus into the dialog.
        const initial = container ? getFocusableElements(container) : [];
        (initial[0] ?? container)?.focus();

        const handleKeyDown = (e: KeyboardEvent) => {
            if (e.key === 'Escape') {
                onCloseRef.current();
                return;
            }
            if (e.key !== 'Tab' || !container) return;

            const focusable = getFocusableElements(container);
            if (focusable.length === 0) {
                // Nothing focusable inside: keep focus pinned to the container.
                e.preventDefault();
                container.focus();
                return;
            }

            const first = focusable[0];
            const last = focusable[focusable.length - 1];
            if (e.shiftKey && document.activeElement === first) {
                e.preventDefault();
                last.focus();
            } else if (!e.shiftKey && document.activeElement === last) {
                e.preventDefault();
                first.focus();
            }
        };

        document.addEventListener('keydown', handleKeyDown);
        return () => {
            document.removeEventListener('keydown', handleKeyDown);
            previouslyFocused?.focus?.();
        };
    }, [isOpen]);

    return containerRef;
}
