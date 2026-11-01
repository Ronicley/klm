import { createContext } from 'react';

export const ActivityPagingContext = createContext<{ hasMore: boolean; older: boolean; next: () => void; latest: () => void }>({ hasMore: false, older: false, next: () => {}, latest: () => {} });
