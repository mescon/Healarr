/* eslint-disable react-refresh/only-export-components -- exports both a hook and a helper component */
import { useState, useCallback } from 'react';
import { type z, type ZodError } from 'zod';

/**
 * Field-level validation errors as a flat object.
 * Keys are field names (including nested paths like "config.host"),
 * values are error messages.
 */
export type FieldErrors = Record<string, string>;

/**
 * Convert Zod's nested error format to a flat field-error map.
 *
 * @example
 * // ZodError for { name: "", config: { host: "" } }
 * // Becomes: { name: "Name is required", "config.host": "Host is required" }
 */
function formatZodErrors(error: ZodError): FieldErrors {
  const errors: FieldErrors = {};

  for (const issue of error.issues) {
    // Join path segments for nested fields
    const path = issue.path.join('.');
    // Only keep the first error for each field
    if (!errors[path]) {
      errors[path] = issue.message;
    }
  }

  return errors;
}

/**
 * Hook for form validation using Zod schemas.
 *
 * Provides:
 * - Field-level error state
 * - validate() function for checking data against schema
 * - validateField() for single-field validation
 * - clearErrors() to reset error state
 * - setFieldError() for manual error setting
 *
 * @example
 * const { errors, validate, clearErrors } = useFormValidation(arrInstanceSchema);
 *
 * const handleSubmit = () => {
 *   if (validate(formData)) {
 *     // Data is valid, proceed with submission
 *   }
 * };
 *
 * // Display inline error
 * {errors.name && <span className="text-red-500">{errors.name}</span>}
 */
export function useFormValidation<T extends z.ZodSchema>(schema: T) {
  const [errors, setErrors] = useState<FieldErrors>({});

  /**
   * Validate data against the schema.
   * Returns true if valid, false if invalid.
   * Updates errors state with any validation errors.
   */
  const validate = useCallback(
    (data: unknown): data is z.infer<T> => {
      const result = schema.safeParse(data);

      if (!result.success) {
        setErrors(formatZodErrors(result.error));
        return false;
      }

      setErrors({});
      return true;
    },
    [schema]
  );

  /**
   * Validate a single field value.
   * Useful for on-blur validation of individual inputs.
   */
  const validateField = useCallback(
    (fieldName: string, value: unknown): boolean => {
      // Create a partial object with just this field
      const partialData = { [fieldName]: value };

      // Try to validate just this field using pick/partial if available
      // For simplicity, we validate the whole schema but only update this field's error
      const result = schema.safeParse(partialData);

      if (!result.success) {
        const fieldErrors = formatZodErrors(result.error);
        const fieldError = fieldErrors[fieldName];

        if (fieldError) {
          setErrors((prev) => ({ ...prev, [fieldName]: fieldError }));
          return false;
        }
      }

      // Clear this field's error if valid
      setErrors((prev) => {
        const next = { ...prev };
        delete next[fieldName];
        return next;
      });
      return true;
    },
    [schema]
  );

  /**
   * Clear all validation errors.
   */
  const clearErrors = useCallback(() => {
    setErrors({});
  }, []);

  /**
   * Clear error for a specific field.
   */
  const clearFieldError = useCallback((fieldName: string) => {
    setErrors((prev) => {
      const next = { ...prev };
      delete next[fieldName];
      return next;
    });
  }, []);

  /**
   * Manually set an error for a field.
   * Useful for server-side validation errors.
   */
  const setFieldError = useCallback((fieldName: string, message: string) => {
    setErrors((prev) => ({ ...prev, [fieldName]: message }));
  }, []);

  /**
   * Check if there are any validation errors.
   */
  const hasErrors = Object.keys(errors).length > 0;

  return {
    errors,
    validate,
    validateField,
    clearErrors,
    clearFieldError,
    setFieldError,
    hasErrors,
  };
}

/**
 * Helper component for displaying inline field errors.
 */
export function FieldError({ error }: { error?: string }) {
  if (!error) return null;

  return (
    <p className="mt-1 text-xs text-red-500 dark:text-red-400" role="alert">
      {error}
    </p>
  );
}
