import { fireEvent, render, screen, waitFor } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { apiGet, apiPost } from '../api/client';
import { useCanvasStore } from '../store/canvas';
import { TrialRunPanel } from './TrialRunPanel';

vi.mock('../api/client', () => ({
  apiGet: vi.fn(),
  apiPost: vi.fn(),
  apiURL: (path: string) => `/api/${path}`,
}));

const mockedGet = vi.mocked(apiGet);
const mockedPost = vi.mocked(apiPost);

describe('TrialRunPanel', () => {
  beforeEach(() => {
    useCanvasStore.getState().setDoc({
      name: 'trial',
      mode: 'fixed_workflow',
      workflow: {
        nodes: [{ id: 'step-a', kind: 'transform' }],
        edges: [],
      },
    });
    mockedPost.mockResolvedValue({
      run_id: 'run-sync',
      status: 'completed',
      structured_output: { answer: 'ok' },
    });
    mockedGet.mockImplementation(async (path: string) => {
      if (path.endsWith('/steps')) {
        return {
          run_id: 'run-sync',
          version: 2,
          status: 'completed',
          steps: [{ node_id: 'step-a', output: { inline: { value: 1 } } }],
        };
      }
      return {
        events: [{
          id: 1,
          sequence: 1,
          event: {
            type: 'RunCompleted',
            run_id: 'run-sync',
            timestamp: '2026-07-25T07:00:00Z',
            display_label: 'Run completed',
          },
          created_at: '2026-07-25T07:00:00Z',
        }],
      };
    });
  });

  it('hydrates output, events, and steps for a synchronously completed trial', async () => {
    render(
      <MemoryRouter>
        <TrialRunPanel />
      </MemoryRouter>,
    );

    fireEvent.click(screen.getByRole('button', { name: /试跑当前图/ }));

    expect(await screen.findByText(/Run completed/)).toBeInTheDocument();
    expect(screen.getByText(/"answer": "ok"/)).toBeInTheDocument();
    expect(screen.getByText('step-a')).toBeInTheDocument();
    expect(screen.getByText(/"value": 1/)).toBeInTheDocument();
    await waitFor(() => expect(mockedGet).toHaveBeenCalledTimes(2));
  });
});
