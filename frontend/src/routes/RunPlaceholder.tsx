import { useRoute } from 'preact-iso';

export function RunPlaceholder() {
  const { params } = useRoute();
  return (
    <div class="p-8 text-neutral-300">
      <div class="text-xs text-neutral-500 mb-2">
        repo <span class="font-mono">{params.fp}</span>
      </div>
      <h2 class="text-lg font-semibold mb-1">
        Run <span class="font-mono">{params.runId}</span>
      </h2>
      <p class="text-sm text-neutral-500">
        Run summary pane lands in RV-009.
      </p>
    </div>
  );
}
