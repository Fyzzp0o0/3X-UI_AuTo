import { useTranslation } from 'react-i18next';
import { Alert, Descriptions, Drawer, Empty, QRCode, Space, Spin, Tag, Typography } from 'antd';

import { useChainNode } from '@/api/queries/useChain';

interface Props {
  nodeId: number | null;
  onClose: () => void;
}

function statusColor(status: string): string {
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

export default function NodeDrawer({ nodeId, onClose }: Props) {
  const { t } = useTranslation();
  const { data, isLoading } = useChainNode(nodeId);
  const node = data?.node;
  const hops = data?.hops ?? [];
  const links = data?.links ?? [];
  const landing = hops.find((hop) => hop.kind === 'landing');
  const fronts = hops.filter((hop) => hop.kind === 'front');

  return (
    <Drawer
      open={nodeId != null}
      onClose={onClose}
      width={560}
      title={node ? node.name : t('pages.chainProxy.nodes.detailTitle')}
      destroyOnHidden
    >
      {isLoading || !node ? (
        <div className="chain-plan-loading">
          <Spin />
        </div>
      ) : (
        <Space direction="vertical" size={16} style={{ width: '100%' }}>
          {node.error ? <Alert type="error" showIcon message={node.error} /> : null}
          <Descriptions size="small" column={1} bordered>
            <Descriptions.Item label={t('status')}>
              <Tag color={statusColor(node.status)}>{node.status}</Tag>
            </Descriptions.Item>
            <Descriptions.Item label={t('pages.chainProxy.nodes.email')}>
              <span className="chain-mono">{node.email}</span>
            </Descriptions.Item>
            <Descriptions.Item label={t('pages.chainProxy.nodes.inbound')}>
              <span className="chain-mono">{node.inboundTag}</span>
            </Descriptions.Item>
            <Descriptions.Item label={t('pages.chainProxy.nodes.outbound')}>
              <span className="chain-mono">{node.outboundTag}</span>
            </Descriptions.Item>
            <Descriptions.Item label={t('pages.chainProxy.nodes.proxy')}>
              <span className="chain-mono">
                {node.proxyHost ? `${node.proxyHost}:${node.proxyPort ?? ''}` : '—'}
              </span>
            </Descriptions.Item>
            <Descriptions.Item label={t('pages.chainProxy.nodes.exitIp')}>
              <span className="chain-mono">{node.exitIp || '—'}</span>
            </Descriptions.Item>
          </Descriptions>

          <div>
            <Typography.Text strong>{t('pages.chainProxy.nodes.chain')}</Typography.Text>
            <div className="chain-topology">
              <Tag>{node.inboundTag}</Tag>
              <span className="chain-arrow">→</span>
              <Tag color="blue">
                {t('pages.chainProxy.nodes.landing')}: {landing?.outboundTag ?? node.outboundTag}
              </Tag>
              {fronts.map((hop) => (
                <span key={hop.position}>
                  <span className="chain-arrow">→</span>
                  <Tag color="purple">
                    {t('pages.chainProxy.nodes.front')}: {hop.outboundTag}
                  </Tag>
                </span>
              ))}
              <span className="chain-arrow">→</span>
              <Tag color="green">{t('pages.chainProxy.nodes.internet')}</Tag>
            </div>
          </div>

          <div>
            <Typography.Text strong>{t('pages.chainProxy.nodes.links')}</Typography.Text>
            {links.length === 0 ? (
              <Empty
                image={Empty.PRESENTED_IMAGE_SIMPLE}
                description={t('pages.chainProxy.nodes.noLinks')}
              />
            ) : (
              <Space direction="vertical" size={12} style={{ width: '100%', marginTop: 8 }}>
                {links.map((link) => (
                  <div key={link} className="chain-link-row">
                    <Typography.Paragraph
                      copyable={{ text: link }}
                      className="chain-mono chain-small"
                    >
                      {link}
                    </Typography.Paragraph>
                    <QRCode value={link} size={96} aria-label={t('pages.chainProxy.nodes.qr')} />
                  </div>
                ))}
              </Space>
            )}
          </div>
        </Space>
      )}
    </Drawer>
  );
}
