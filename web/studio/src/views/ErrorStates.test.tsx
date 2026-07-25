import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { fireEvent, render, screen } from '@testing-library/react';
import { MemoryRouter, Route, Routes } from 'react-router';
import { beforeEach, describe, expect, it, vi } from 'vitest';
import { apiGet } from '../api/client';
import { PartsPalette } from '../components/PartsPalette';
import { RunDetailView } from './RunDetailView';
import { RunsView } from './RunsView';

vi.mock('../api/client', () => ({
  apiGet: vi.fn(),
  apiPost: vi.fn(),
}));

const mockedGet = vi.mocked(apiGet);

function queryClient() {
  return new QueryClient({
    defaultOptions: {
      queries: { retry: false, refetchOnWindowFocus: false },
    },
  });
}

describe('Studio query errors', () => {
  beforeEach(() => {
    mockedGet.mockRejectedValue(new Error('backend unavailable'));
  });

  it('shows an explicit error instead of an empty runs state', async () => {
    render(
      <QueryClientProvider client={queryClient()}>
        <MemoryRouter>
          <RunsView />
        </MemoryRouter>
      </QueryClientProvider>,
    );

    expect(await screen.findByText('运行记录加载失败')).toBeInTheDocument();
    expect(screen.queryByText('还没有运行记录')).not.toBeInTheDocument();
  });

  it('shows errors for run trace and steps tabs', async () => {
    render(
      <QueryClientProvider client={queryClient()}>
        <MemoryRouter initialEntries={['/runs/run-1']}>
          <Routes>
            <Route path="/runs/:runID" element={<RunDetailView />} />
          </Routes>
        </MemoryRouter>
      </QueryClientProvider>,
    );

    expect(await screen.findByText('Trace 加载失败')).toBeInTheDocument();
    fireEvent.click(screen.getByRole('button', { name: 'Steps' }));
    expect(await screen.findByText('Steps 加载失败')).toBeInTheDocument();
  });

  it('shows an explicit parts loading error', async () => {
    render(
      <QueryClientProvider client={queryClient()}>
        <PartsPalette />
      </QueryClientProvider>,
    );
    expect(await screen.findByText('零件加载失败')).toBeInTheDocument();
  });
});
