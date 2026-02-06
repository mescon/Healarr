import type { RootFolder, EventGroup } from '../../lib/api';

export type WizardStep = 'welcome' | 'password' | 'arr' | 'path' | 'notifications' | 'complete';

export interface ArrFormData {
    name: string;
    type: 'sonarr' | 'radarr' | 'whisparr-v2' | 'whisparr-v3';
    url: string;
    api_key: string;
}

export interface PathFormData {
    local_path: string;
    arr_path: string;
    arr_instance_id: number | null;
}

export interface NotificationFormData {
    name: string;
    provider_type: string;
    config: Record<string, unknown>;
    events: string[];
    enabled: boolean;
    throttle_seconds: number;
}

export const STEPS: WizardStep[] = ['welcome', 'password', 'arr', 'path', 'notifications', 'complete'];

export const DEFAULT_ARR_DATA: ArrFormData = {
    name: '',
    type: 'sonarr',
    url: '',
    api_key: '',
};

export const DEFAULT_PATH_DATA: PathFormData = {
    local_path: '',
    arr_path: '',
    arr_instance_id: null,
};

export const DEFAULT_NOTIFICATION_DATA: NotificationFormData = {
    name: '',
    provider_type: 'none',
    config: {},
    events: ['CorruptionDetected', 'ScanComplete'],
    enabled: true,
    throttle_seconds: 300,
};

// Props interfaces for step components

export interface WelcomeStepProps {
    onFreshStart: () => void;
    onRestoreComplete: () => Promise<void>;
    onSkip: () => void;
}

export interface PasswordStepProps {
    onNext: (token?: string) => void;
    onBack: () => void;
}

export interface ArrStepProps {
    initialData?: ArrFormData;
    initialTested?: boolean;
    onNext: (createdId: number) => void;
    onBack: () => void;
    onSkip: () => void;
}

export interface PathStepProps {
    initialData?: PathFormData;
    rootFolders: RootFolder[];
    loadingRootFolders: boolean;
    arrInstanceId: number | null;
    onNext: () => void;
    onBack: () => void;
    onSkip: () => void;
}

export interface NotificationsStepProps {
    initialData?: NotificationFormData;
    initialTested?: boolean;
    eventGroups: EventGroup[];
    onNext: () => void;
    onBack: () => void;
    onSkip: () => void;
}

export interface CompleteStepProps {
    onComplete: () => void;
}
