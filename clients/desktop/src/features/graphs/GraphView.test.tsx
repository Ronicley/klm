import { act, render } from '@testing-library/react';
import { describe, expect, it, vi } from 'vitest';
import type { NodeChange } from '@xyflow/react';
import type { CanvasNode } from './GraphCanvas';
import type { GraphFile } from './files';
import { GraphView } from './GraphView';

const flow = vi.hoisted(() => ({ props: null as unknown as { nodes: CanvasNode[]; onNodesChange: (changes: NodeChange<CanvasNode>[]) => void } }));
vi.mock('@xyflow/react', () => ({
  ReactFlow: (props: typeof flow.props) => { flow.props = props; return null; },
  Background: () => null, Panel: () => null, MarkerType: { ArrowClosed: 'arrowclosed' },
}));
vi.mock('./GraphCanvas', () => ({ CanvasControls: () => null, graphViewNodeTypes: {} }));

describe('GraphView measurements', () => {
  it('retains measured nodes across live updates and hidden-tab observations', () => {
    const graph: GraphFile = { id: 'graph', revision: '1', layout: { positions: {} }, definition: { name: 'Graph', description: '', enabled: true, initial_node: 'agent', nodes: { agent: { type: 'agent', agent: 'worker' } }, choices: {} } };
    const props = { sessionId: 'chat', graph, agents: [], visible: true, requests: [], windowFocused: true, parentFocused: false, onRequestTarget: vi.fn(), controlPending: false, controlError: '', onControl: vi.fn() };
    const { rerender } = render(<GraphView {...props} />);
    act(() => flow.props.onNodesChange([{ type: 'dimensions', id: 'agent', dimensions: { width: 280, height: 140 } }]));
    const measured = flow.props.nodes[0].measured;
    rerender(<GraphView {...props} windowFocused={false} graph={{ ...graph, revision: '2' }} />);
    expect(flow.props.nodes[0].measured).toBe(measured);
    rerender(<GraphView {...props} visible={false} />);
    act(() => flow.props.onNodesChange([{ type: 'dimensions', id: 'agent', dimensions: { width: 0, height: 0 } }]));
    rerender(<GraphView {...props} />);
    expect(flow.props.nodes[0].measured).toEqual({ width: 280, height: 140 });
  });
});
