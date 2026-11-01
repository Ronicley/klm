import { createContext } from 'react';

// Stable graph/node identity for the subeditors; never contains callbacks or DOM.
export const GraphDraftScope = createContext('form.graphPreview');
