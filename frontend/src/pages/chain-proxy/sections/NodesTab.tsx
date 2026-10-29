import { useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Button, Card, Select, Space, Table, Tag, Tooltip, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';

import { getMessage } from '@/utils/messageBus';
import { useChainMutations, useChainNodes, useChainPools } from '@/api/queries/useChain';
import type { ChainNode } from '@/schemas/chain';

function nodeColor(status: string): string {
  switch (status) {
    case 'active':
      return 'green';
    case 'degraded':
      return 'orange';
    case 'failed':
      return 'red';
    case 'disabled':
      return 'default';
    default:
      return 'blue';
  }
}

export default function NodesTab() {
  const { t } = useTranslation();
  const { pools } = useChainPools();
  const [poolId, setPoolId] = useState<number | undefined>(undefined);
  const [status, setStatus] = useState<string | undefined>(undefined);
  const [page, setPage] = useState(1);
  const { data, isLoading } = useChainNodes({ poolId, status, page, size: 50 });
  const { setNodeEnabled, rebindNode } = useChainMutations();

  const toggle = async (node: ChainNode) => {
    const enable = node.status === 'disabled' || !node.clientEnabled;
    try {
      await setNodeEnabled({ id: node.id, enable });
      getMessage().success(
        t(enable ? 'pages.chainProxy.toasts.enabled' : 'pages.chainProxy.toasts.disabled'),
      );
    } catch (error) {
      getMessage().error((error as Error).message);
    }
  };

  const rebind = async (node: ChainNode) => {
    try {
      await rebindNode(node.id);
      getMessage().success(t('pages.chainProxy.toasts.rebound'));
    } catch (error) {
      getMessage().error((error as Error).message);
    }
  };

  const columns: ColumnsType<ChainNode> = [
    {
      title: t('pages.chainProxy.nodes.name'),
      dataIndex: 'name',
      render: (name: string, node) => (
        <Space direction="vertical" size={0}>
          <Typography.Text strong>{name}</Typography.Text>
          <Typography.Text type="secondary" className="chain-mono chain-small">
            {node.email}
          </Typography.Text>
        </Space>
      ),
    },
    {
      title: t('status'),
      dataIndex: 'status',
      width: 110,
      render: (value: string, node) =>
        node.error ? (
          <Tooltip title={node.error}>
            <Tag color={nodeColor(value)}>{value}</Tag>
          </Tooltip>
        ) : (
          <Tag color={nodeColor(value)}>{value}</Tag>
        ),
    },
    {
      title: t('pages.chainProxy.nodes.proxy'),
      width: 190,
      render: (_, node) => (
        <Space direction="vertical" size={0}>
          <span className="chain-mono">
            {node.proxyHost ? `${node.proxyHost}:${node.proxyPort ?? ''}` : '—'}
          </span>
          <Tag color={node.health === 'healthy' ? 'green' : 'default'}>{node.health ?? '—'}</Tag>
        </Space>
      ),
    },
    {
      title: t('pages.chainProxy.nodes.latency'),
      width: 100,
      render: (_, node) => (node.latencyMs ? `${node.latencyMs} ms` : '—'),
    },
    {
      title: t('pages.chainProxy.nodes.exitIp'),
      width: 150,
      render: (_, node) => <span className="chain-mono">{node.exitIp || '—'}</span>,
    },
    {
      title: t('pages.chainProxy.nodes.inbound'),
      width: 160,
      render: (_, node) => <span className="chain-mono chain-small">{node.inboundTag}</span>,
    },
    {
      title: t('pages.chainProxy.nodes.outbound'),
      width: 170,
      render: (_, node) => <span className="chain-mono chain-small">{node.outboundTag}</span>,
    },
    {
      title: t('pages.chainProxy.pools.actions'),
      width: 170,
      render: (_, node) => (
        <Space size={4}>
          <Button size="small" onClick={() => toggle(node)}>
            {node.status === 'disabled' || !node.clientEnabled
              ? t('pages.chainProxy.nodes.enable')
              : t('pages.chainProxy.nodes.disable')}
          </Button>
          <Button size="small" onClick={() => rebind(node)}>
            {t('pages.chainProxy.nodes.rebind')}
          </Button>
        </Space>
      ),
    },
  ];

  return (
    <Card size="small" title={t('pages.chainProxy.nodes.title')}>
      <Space style={{ marginBottom: 12 }} wrap>
        <Select
          allowClear
          aria-label={t('pages.chainProxy.nodes.filterPool')}
          style={{ minWidth: 180 }}
          placeholder={t('pages.chainProxy.nodes.filterPool')}
          value={poolId}
          onChange={(value?: number) => {
            setPoolId(value);
            setPage(1);
          }}
          options={pools.map((pool) => ({ value: pool.id, label: pool.name }))}
        />
        <Select
          allowClear
          aria-label={t('pages.chainProxy.nodes.filterStatus')}
          style={{ minWidth: 160 }}
          placeholder={t('pages.chainProxy.nodes.filterStatus')}
          value={status}
          onChange={(value?: string) => {
            setStatus(value);
            setPage(1);
          }}
          options={['provisioning', 'active', 'degraded', 'failed', 'disabled'].map((value) => ({
            value,
            label: value,
          }))}
        />
      </Space>
      <Table<ChainNode>
        rowKey="id"
        size="small"
        loading={isLoading}
        columns={columns}
        dataSource={data?.items ?? []}
        scroll={{ x: 1000 }}
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
