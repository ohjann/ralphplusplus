import { useRoute } from 'preact-iso';
import { ChatView } from '../components/ChatView/ChatView';

export function IterRoute() {
  const { params } = useRoute();
  const { fp, runId, story, iter } = params;
  if (!fp || !runId || !story || !iter) return null;
  return <ChatView fp={fp} runId={runId} story={story} iter={iter} />;
}
