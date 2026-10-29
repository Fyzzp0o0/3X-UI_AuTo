import { useTranslation } from 'react-i18next';
import { Card, Progress, Space, Table, Tag, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';

import { useChainJob, useChainJobs } from '@/api/queries/useChain';
import type { ChainJob } from '@/schemas/chain';

interface Props {
  highlightJobId: number | null;
}

function jobColor(status: string): string {
  switch (status) {
    case 'succeeded':
      return 'green';
    case 'failed':
      return 'red';
    case 'partial':
      return 'orange';
    case 'running':
      return 'blue';
    default:
      return 'default';
  }
}

function formatTime(ms: number): string {
  if (!ms) return '—';
  return new Intl.DateTimeFormat(undefined, {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  }).format(new Date(ms));
}

export default function JobsTab({ highlightJobId }: Props) {
  const { t } = useTranslation();
  const { data: jobs = [], isLoading } = useChainJobs();
  const { data: highlighted } = useChainJob(highlightJobId);

  const columns: ColumnsType<ChainJob> = [
    { title: '#', dataIndex: 'id', width: 70 },
    {
      title: t('pages.chainProxy.jobs.kind'),
      dataIndex: 'kind',
      width: 110,
      render: (kind: string) => <Tag>{kind}</Tag>,
    },
    {
      title: t('status'),
      dataIndex: 'status',
      width: 110,
      render: (status: string) => <Tag color={jobColor(status)}>{status}</Tag>,
    },
    {
      title: t('pages.chainProxy.jobs.progress'),
      width: 160,
      render: (_, job) => (
        <Progress
          percent={
            job.total > 0
              ? Math.round((job.done / job.total) * 100)
              : job.status === 'succeeded'
                ? 100
                : 0
          }
          size="small"
          status={job.status === 'failed' ? 'exception' : undefined}
          format={() => `${job.done}/${job.total}`}
        />
      ),
    },
    {
      title: t('pages.chainProxy.jobs.message'),
      dataIndex: 'message',
      ellipsis: true,
      render: (message: string) => message || '—',
    },
    {
      title: t('pages.chainProxy.jobs.created'),
      dataIndex: 'createdAt',
      width: 170,
      render: (value: number) => (
        <span className="chain-mono chain-small">{formatTime(value)}</span>
      ),
    },
  ];

  return (
    <Space direction="vertical" size={16} style={{ width: '100%' }}>
      {highlighted ? (
        <Card size="small" title={t('pages.chainProxy.jobs.current', { id: highlighted.id })}>
          <Space direction="vertical" style={{ width: '100%' }}>
            <Space>
              <Tag color={jobColor(highlighted.status)}>{highlighted.status}</Tag>
              <Tag>{highlighted.kind}</Tag>
              <Typography.Text type="secondary">
                {formatTime(highlighted.createdAt)}
              </Typography.Text>
            </Space>
            <Progress
              percent={
                highlighted.total > 0 ? Math.round((highlighted.done / highlighted.total) * 100) : 0
              }
              status={highlighted.status === 'failed' ? 'exception' : 'active'}
            />
            {highlighted.message ? (
              <Typography.Text type={highlighted.status === 'failed' ? 'danger' : 'secondary'}>
                {highlighted.message}
              </Typography.Text>
            ) : null}
          </Space>
        </Card>
      ) : null}
      <Card size="small" title={t('pages.chainProxy.jobs.title')}>
        <Table<ChainJob>
          rowKey="id"
          size="small"
          loading={isLoading}
          columns={columns}
          dataSource={jobs}
          pagination={{ pageSize: 20, hideOnSinglePage: true }}
          scroll={{ x: 760 }}
        />
      </Card>
    </Space>
  );
}
