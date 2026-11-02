import { createContext } from 'react';

export const ActivityPagingContext = createContext<{ hasMore: boolean; cursor?: string; busy?: boolean; error?: string; older: boolean; next: () => void; latest: () => void }>({ hasMore: false, older: false, next: () => {}, latest: () => {} });
