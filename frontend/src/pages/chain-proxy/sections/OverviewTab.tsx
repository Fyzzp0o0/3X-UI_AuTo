import { useTranslation } from 'react-i18next';
import { Button, Card, Col, Empty, Progress, Row, Space, Statistic, Tag, Typography } from 'antd';
import {
  CheckCircleOutlined,
  ClusterOutlined,
  GlobalOutlined,
  ThunderboltOutlined,
} from '@ant-design/icons';

import { useChainPools } from '@/api/queries/useChain';

interface Props {
  onOpenPools: () => void;
}

export default function OverviewTab({ onOpenPools }: Props) {
  const { t } = useTranslation();
  const { pools, fetched } = useChainPools();

  const totals = pools.reduce(
    (acc, p) => ({
      proxies: acc.proxies + (p.proxies ?? 0),
      healthy: acc.healthy + (p.healthy ?? 0),
      nodes: acc.nodes + (p.nodes ?? 0),
    }),
    { proxies: 0, healthy: 0, nodes: 0 },
  );

  return (
    <Space direction="vertical" size={16} style={{ width: '100%' }}>
      <Row gutter={[16, 16]}>
        <Col xs={12} md={6}>
          <Card size="small" className="chain-stat-card">
            <Statistic
              title={t('pages.chainProxy.summary.pools')}
              value={pools.length}
              prefix={<ClusterOutlined aria-hidden />}
            />
          </Card>
        </Col>
        <Col xs={12} md={6}>
          <Card size="small" className="chain-stat-card">
            <Statistic
              title={t('pages.chainProxy.summary.proxies')}
              value={totals.proxies}
              prefix={<GlobalOutlined aria-hidden />}
            />
          </Card>
        </Col>
        <Col xs={12} md={6}>
          <Card size="small" className="chain-stat-card">
            <Statistic
              title={t('pages.chainProxy.summary.healthy')}
              value={totals.healthy}
              prefix={
                <CheckCircleOutlined aria-hidden style={{ color: 'var(--ant-color-success)' }} />
              }
            />
          </Card>
        </Col>
        <Col xs={12} md={6}>
          <Card size="small" className="chain-stat-card">
            <Statistic
              title={t('pages.chainProxy.summary.nodes')}
              value={totals.nodes}
              prefix={<ThunderboltOutlined aria-hidden />}
            />
          </Card>
        </Col>
      </Row>

      <Card
        size="small"
        title={t('pages.chainProxy.pools.title')}
        extra={
          <Button type="primary" size="small" onClick={onOpenPools}>
            {t('pages.chainProxy.pools.manage')}
          </Button>
        }
      >
        {!fetched ? null : pools.length === 0 ? (
          <Empty description={t('pages.chainProxy.pools.empty')} />
        ) : (
          <Row gutter={[16, 16]}>
            {pools.map((pool) => {
              const health = pool.proxies
                ? Math.round(((pool.healthy ?? 0) / pool.proxies) * 100)
                : 0;
              return (
                <Col xs={24} md={12} xl={8} key={pool.id}>
                  <Card size="small" className="chain-pool-card" hoverable onClick={onOpenPools}>
                    <Space direction="vertical" size={4} style={{ width: '100%' }}>
                      <Space style={{ justifyContent: 'space-between', width: '100%' }}>
                        <Typography.Text strong>{pool.name}</Typography.Text>
                        <Tag color={pool.enabled ? 'green' : 'default'}>
                          {pool.enabled
                            ? t('pages.chainProxy.pools.enabled')
                            : t('pages.chainProxy.pools.disabled')}
                        </Tag>
                      </Space>
                      <Typography.Text type="secondary" className="chain-mono">
                        {pool.tagPrefix} · {pool.mode} · {pool.shardSize}/
                        {t('pages.chainProxy.pools.shard')}
                      </Typography.Text>
                      <Progress
                        percent={health}
                        size="small"
                        status={health >= 80 ? 'success' : health >= 40 ? 'normal' : 'exception'}
                        format={(percent) => `${percent ?? 0}%`}
                      />
                      <Typography.Text type="secondary">
                        {t('pages.chainProxy.pools.counters', {
                          proxies: pool.proxies ?? 0,
                          nodes: pool.nodes ?? 0,
                        })}
                      </Typography.Text>
                    </Space>
                  </Card>
                </Col>
              );
            })}
          </Row>
        )}
      </Card>
    </Space>
  );
}
