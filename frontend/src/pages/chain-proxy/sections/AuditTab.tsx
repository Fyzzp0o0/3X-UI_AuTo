import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Card, Table, Tag, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';

import { useChainAudit } from '@/api/queries/useChain';
import type { ChainAudit } from '@/schemas/chain';

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

function actionColor(action: string): string {
  if (action.includes('delete')) return 'red';
  if (action.includes('failover') || action.includes('rebind')) return 'orange';
  if (action.includes('import')) return 'green';
  return 'blue';
}

export default function AuditTab() {
  const { t } = useTranslation();
  const [page, setPage] = useState(1);
  const { data, isLoading } = useChainAudit(page, 50);

  const columns: ColumnsType<ChainAudit> = [
    {
      title: t('pages.chainProxy.audit.time'),
      dataIndex: 'at',
      width: 170,
      render: (at: number) => <span className="chain-mono chain-small">{formatTime(at)}</span>,
    },
    { title: t('pages.chainProxy.audit.actor'), dataIndex: 'actor', width: 120, ellipsis: true },
    {
      title: t('pages.chainProxy.audit.action'),
      dataIndex: 'action',
      width: 160,
      render: (action: string) => <Tag color={actionColor(action)}>{action}</Tag>,
    },
    {
      title: t('pages.chainProxy.audit.object'),
      width: 140,
      render: (_, row) =>
        row.objectId > 0 ? (
          <span className="chain-mono chain-small">
            {row.objectType}#{row.objectId}
          </span>
        ) : (
          <span className="chain-mono chain-small">{row.objectType || '—'}</span>
        ),
    },
    {
      title: t('pages.chainProxy.audit.ip'),
      dataIndex: 'ip',
      width: 140,
      render: (ip: string) => <span className="chain-mono chain-small">{ip || '—'}</span>,
    },
    {
      title: t('pages.chainProxy.audit.detail'),
      dataIndex: 'detail',
      ellipsis: true,
      render: (detail: string) => detail || '—',
    },
  ];

  return (
    <Card size="small" title={t('pages.chainProxy.audit.title')}>
      <Typography.Paragraph type="secondary" className="chain-small">
        {t('pages.chainProxy.audit.hint')}
      </Typography.Paragraph>
      <Table<ChainAudit>
        rowKey="id"
        size="small"
        loading={isLoading}
        columns={columns}
        dataSource={data?.items ?? []}
        scroll={{ x: 900 }}
        pagination={{
          current: page,
          pageSize: 50,
          total: data?.total ?? 0,
          onChange: setPage,
          showSizeChanger: false,
        }}
      />
    </Card>
  );
}
