import { ListTodo } from 'lucide-react';
import { Button } from '../../design-system/Button';
import './activity-board.css';

export function ActivityBoardEntry({ onOpen }: { onOpen: () => void }) {
  return <section className="activity-board-entry"><h3><ListTodo aria-hidden="true" />Tasks</h3><Button size="sm" onClick={onOpen}>Open tasks</Button></section>;
}
