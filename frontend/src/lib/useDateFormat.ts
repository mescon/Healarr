import { useState, useEffect, useCallback } from 'react';
import { 
  getDateFormatPreset, 
  setDateFormatPreset as setPreset,
  formatTime,
  formatDate,
  formatDateTimeFull,
  formatDateTimeCompact,
  type DateFormatPreset 
} from './dateFormat';

// Re-export the type for convenience
export type { DateFormatPreset } from './dateFormat';

export const useDateFormat = () => {
  const [preset, setPresetState] = useState<DateFormatPreset>(getDateFormatPreset);

  useEffect(() => {
    const handleChange = (e: CustomEvent<DateFormatPreset>) => {
      setPresetState(e.detail);
    };
    
    window.addEventListener('dateFormatChange', handleChange as EventListener);
    return () => window.removeEventListener('dateFormatChange', handleChange as EventListener);
  }, []);

  const setDateFormatPreset = useCallback((newPreset: DateFormatPreset) => {
    setPreset(newPreset);
    setPresetState(newPreset);
  }, []);

  return {
    preset,
    setDateFormatPreset,
    // Time is always on top in table views
    formatTime,
    // Date is always below in table views (format respects preset)
    formatDate,
    // Full format for tooltips
    formatFull: formatDateTimeFull,
    // Compact format for limited space
    formatCompact: formatDateTimeCompact,
  };
};
