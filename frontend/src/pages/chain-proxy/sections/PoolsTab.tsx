import { useMemo, useState } from 'react';
import { useTranslation } from 'react-i18next';
import {
  Button,
  Card,
  Drawer,
  Form,
  Input,
  InputNumber,
  Modal,
  Select,
  Space,
  Switch,
  Table,
  Tag,
  Tooltip,
  Typography,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import {
  DeleteOutlined,
  DownloadOutlined,
  PlayCircleOutlined,
  PlusOutlined,
  RadarChartOutlined,
  SwapOutlined,
} from '@ant-design/icons';
import { FormProvider, useForm, useWatch } from 'react-hook-form';
import type { z } from 'zod';

import { FormField, rhfZodValidate } from '@/components/form/rhf';
import { getMessage } from '@/utils/messageBus';
import { useChainMutations, useChainPools, useChainProxies } from '@/api/queries/useChain';
import {
  PoolChainStrategySchema,
  PoolClientTemplateSchema,
  PoolFormSchema,
  PoolInboundTemplateSchema,
  PoolProbePolicySchema,
  type ChainMeta,
  type ChainPool,
  type ChainProxy,
  type PoolFormValues,
} from '@/schemas/chain';
import ImportDrawer from '../wizard/ImportDrawer';
import PlanModal from '../wizard/PlanModal';

interface Props {
  meta?: ChainMeta;
  onJobStarted: (jobId: number) => void;
}

function parseTemplate<S extends z.ZodTypeAny>(schema: S, raw: string): z.infer<S> | undefined {
  try {
    const parsed = schema.safeParse(JSON.parse(raw || '{}'));
    return parsed.success ? parsed.data : undefined;
  } catch {
    return undefined;
  }
}

function poolToForm(pool: ChainPool): PoolFormValues {
  const inbound = parseTemplate(PoolInboundTemplateSchema, pool.inboundTemplate);
  const client = parseTemplate(PoolClientTemplateSchema, pool.clientTemplate);
  const chain = parseTemplate(PoolChainStrategySchema, pool.chainStrategy);
  const probe = parseTemplate(PoolProbePolicySchema, pool.probePolicy);
  const reality = inbound?.reality;
  const mode = ['shared', 'sharded', 'dedicated'].includes(pool.mode)
    ? (pool.mode as PoolFormValues['mode'])
    : 'sharded';
  return {
    name: pool.name,
    mode,
    shardSize: pool.shardSize,
    basePort: inbound?.basePort ?? 2083,
    portEnd: inbound?.portEnd ?? 0,
    security:
      inbound?.security === 'tls' || inbound?.security === 'reality' ? inbound.security : 'none',
    network:
      inbound?.network === 'ws' ||
      inbound?.network === 'grpc' ||
      inbound?.network === 'httpupgrade' ||
      inbound?.network === 'xhttp'
        ? inbound.network
        : 'tcp',
    path: inbound?.transport?.path ?? '',
    transportHost: inbound?.transport?.host ?? '',
    tlsCert: inbound?.tls?.certificateFile ?? '',
    tlsKey: inbound?.tls?.keyFile ?? '',
    tlsServerName: inbound?.tls?.serverName ?? '',
    realityDest: reality?.dest ?? '',
    realityServerNames: (reality?.serverNames ?? []).join(','),
    realityShortIds: (reality?.shortIds ?? []).join(','),
    realityPrivateKey: reality?.privateKey ?? '',
    realityFingerprint: reality?.fingerprint ?? 'chrome',
    totalGB: client?.totalGB ?? 0,
    group: client?.group ?? '',
    frontTag: chain?.frontTag ?? '',
    autoFailover: chain?.autoFailover ?? false,
    probeUrl: probe?.url ?? '',
  };
}

function healthColor(health: string): string {
  switch (health) {
    case 'healthy':
      return 'green';
    case 'degraded':
      return 'orange';
    case 'unhealthy':
      return 'red';
    case 'disabled':
      return 'default';
    default:
      return 'blue';
  }
}

export default function PoolsTab({ meta, onJobStarted }: Props) {
  const { t } = useTranslation();
  const { pools, fetched } = useChainPools();
  const { createPool, creating, updatePool, updating, deletePool, probe, failover } =
    useChainMutations();
  const [modal, modalContextHolder] = Modal.useModal();
  const [createOpen, setCreateOpen] = useState(false);
  const [editing, setEditing] = useState<ChainPool | null>(null);
  const [importPool, setImportPool] = useState<ChainPool | null>(null);
  const [planPool, setPlanPool] = useState<ChainPool | null>(null);
  const [proxyPool, setProxyPool] = useState<ChainPool | null>(null);

  const methods = useForm<PoolFormValues>({
    defaultValues: {
      name: '',
      mode: 'sharded',
      shardSize: 40,
      basePort: meta?.defaultPort ?? 2083,
      totalGB: 100,
      group: '',
      frontTag: '',
      probeUrl: '',
      portEnd: 0,
      security: 'none',
      network: 'tcp',
      path: '',
      transportHost: '',
      tlsCert: '',
      tlsKey: '',
      tlsServerName: '',
      realityDest: '',
      realityServerNames: '',
      realityShortIds: '',
      realityPrivateKey: '',
      realityFingerprint: 'chrome',
    },
  });

  const security = useWatch({ control: methods.control, name: 'security' });
  const network = useWatch({ control: methods.control, name: 'network' });

  const openCreate = () => {
    setEditing(null);
    methods.reset({
      name: '',
      mode: 'sharded',
      shardSize: 40,
      basePort: meta?.defaultPort ?? 2083,
      totalGB: 100,
      group: '',
      frontTag: '',
      probeUrl: '',
      portEnd: 0,
      security: 'none',
      network: 'tcp',
      path: '',
      transportHost: '',
      tlsCert: '',
      tlsKey: '',
      tlsServerName: '',
      realityDest: '',
      realityServerNames: '',
      realityShortIds: '',
      realityPrivateKey: '',
      realityFingerprint: 'chrome',
    });
    setCreateOpen(true);
  };

  const openEdit = (pool: ChainPool) => {
    setEditing(pool);
    methods.reset(poolToForm(pool));
    setCreateOpen(true);
  };

  const submitCreate = methods.handleSubmit(async (values) => {
    try {
      if (editing) {
        await updatePool({ id: editing.id, values });
        getMessage().success(t('pages.chainProxy.toasts.updated'));
      } else {
        await createPool(values);
        getMessage().success(t('pages.chainProxy.toasts.created'));
      }
      setCreateOpen(false);
    } catch (error) {
      getMessage().error((error as Error).message);
    }
  });

  const onFailover = async (pool: ChainPool) => {
    try {
      const job = await failover(pool.id);
      getMessage().success(t('pages.chainProxy.plan.queued', { id: job.id }));
      onJobStarted(job.id);
    } catch (error) {
      getMessage().error((error as Error).message);
    }
  };

  const onProbe = async (pool: ChainPool) => {
    try {
      const job = await probe(pool.id);
      getMessage().success(t('pages.chainProxy.plan.queued', { id: job.id }));
      onJobStarted(job.id);
    } catch (error) {
      getMessage().error((error as Error).message);
    }
  };

  const onDelete = (pool: ChainPool) => {
    modal.confirm({
      title: t('pages.chainProxy.pools.deleteTitle', { name: pool.name }),
      content: t('pages.chainProxy.pools.deleteHint'),
      okButtonProps: { danger: true },
      onOk: async () => {
        try {
          await deletePool(pool.id);
          getMessage().success(t('pages.chainProxy.toasts.deleted'));
        } catch (error) {
          getMessage().error((error as Error).message);
          throw error;
        }
      },
    });
  };

  const columns: ColumnsType<ChainPool> = [
    {
      title: t('pages.chainProxy.pools.name'),
      dataIndex: 'name',
      render: (name: string, pool) => (
        <Space direction="vertical" size={0}>
          <Typography.Text strong>{name}</Typography.Text>
          <Typography.Text type="secondary" className="chain-mono chain-small">
            {pool.tagPrefix}
          </Typography.Text>
        </Space>
      ),
    },
    {
      title: t('pages.chainProxy.pools.mode'),
      dataIndex: 'mode',
      width: 120,
      render: (mode: string, pool) => (
        <Space direction="vertical" size={0}>
          <Tag>{mode}</Tag>
          <Typography.Text type="secondary" className="chain-small">
            {pool.shardSize} / {t('pages.chainProxy.pools.shard')}
          </Typography.Text>
        </Space>
      ),
    },
    {
      title: t('pages.chainProxy.pools.proxies'),
      width: 130,
      render: (_, pool) => (
        <Space size={4}>
          <Typography.Text>{pool.proxies ?? 0}</Typography.Text>
          <Tag color="green">{pool.healthy ?? 0}</Tag>
        </Space>
      ),
    },
    {
      title: t('pages.chainProxy.pools.nodes'),
      dataIndex: 'nodes',
      width: 90,
      render: (nodes?: number) => nodes ?? 0,
    },
    {
      title: t('status'),
      dataIndex: 'enabled',
      width: 90,
      render: (enabled: boolean) => (
        <Tag color={enabled ? 'green' : 'default'}>
          {enabled ? t('pages.chainProxy.pools.enabled') : t('pages.chainProxy.pools.disabled')}
        </Tag>
      ),
    },
    {
      title: t('pages.chainProxy.pools.actions'),
      width: 320,
      render: (_, pool) => (
        <Space size={4} wrap>
          <Button
            size="small"
            icon={<DownloadOutlined aria-hidden />}
            onClick={() => setImportPool(pool)}
          >
            {t('pages.chainProxy.pools.import')}
          </Button>
          <Button size="small" onClick={() => setProxyPool(pool)}>
            {t('pages.chainProxy.pools.proxies')}
          </Button>
          <Button size="small" onClick={() => openEdit(pool)}>
            {t('pages.chainProxy.pools.edit')}
          </Button>
          <Button
            size="small"
            icon={<RadarChartOutlined aria-hidden />}
            onClick={() => onProbe(pool)}
          >
            {t('pages.chainProxy.pools.probe')}
          </Button>
          <Button size="small" icon={<SwapOutlined aria-hidden />} onClick={() => onFailover(pool)}>
            {t('pages.chainProxy.pools.failover')}
          </Button>
          <Button
            size="small"
            type="primary"
            icon={<PlayCircleOutlined aria-hidden />}
            onClick={() => setPlanPool(pool)}
          >
            {t('pages.chainProxy.pools.generate')}
          </Button>
          <Tooltip title={t('pages.chainProxy.pools.delete')}>
            <Button
              size="small"
              danger
              icon={<DeleteOutlined aria-hidden />}
              onClick={() => onDelete(pool)}
            />
          </Tooltip>
        </Space>
      ),
    },
  ];

  return (
    <Card
      size="small"
      title={t('pages.chainProxy.pools.title')}
      extra={
        <Button type="primary" icon={<PlusOutlined aria-hidden />} onClick={openCreate}>
          {t('pages.chainProxy.pools.add')}
        </Button>
      }
    >
      {modalContextHolder}
      <Table<ChainPool>
        rowKey="id"
        size="small"
        loading={!fetched}
        columns={columns}
        dataSource={pools}
        pagination={{ pageSize: 20, hideOnSinglePage: true }}
        scroll={{ x: 900 }}
      />

      <Modal
        open={createOpen}
        title={editing ? t('pages.chainProxy.pools.edit') : t('pages.chainProxy.pools.add')}
        onCancel={() => setCreateOpen(false)}
        onOk={submitCreate}
        confirmLoading={creating || updating}
        destroyOnHidden
        width={560}
      >
        <FormProvider {...methods}>
          <Form layout="vertical" colon={false}>
            <FormField
              name="name"
              label={t('pages.chainProxy.pools.name')}
              rules={{ validate: rhfZodValidate(PoolFormSchema.shape.name) }}
            >
              <Input placeholder="pool-hk" />
            </FormField>
            <Space size={12} style={{ width: '100%' }} align="start">
              <FormField name="mode" label={t('pages.chainProxy.pools.mode')}>
                <Select
                  options={[
                    { value: 'sharded', label: 'sharded' },
                    { value: 'shared', label: 'shared' },
                    { value: 'dedicated', label: 'dedicated' },
                  ]}
                />
              </FormField>
              <FormField name="shardSize" label={t('pages.chainProxy.pools.shardSize')}>
                <InputNumber min={1} max={500} />
              </FormField>
              <FormField name="basePort" label={t('pages.chainProxy.pools.basePort')}>
                <InputNumber min={1} max={65535} />
              </FormField>
            </Space>
            <Space size={12} style={{ width: '100%' }} align="start">
              <FormField name="security" label={t('pages.chainProxy.pools.security')}>
                <Select
                  options={['none', 'tls', 'reality'].map((value) => ({ value, label: value }))}
                />
              </FormField>
              <FormField name="network" label={t('pages.chainProxy.pools.network')}>
                <Select
                  options={['tcp', 'ws', 'grpc', 'httpupgrade', 'xhttp'].map((value) => ({
                    value,
                    label: value,
                  }))}
                />
              </FormField>
              <FormField name="portEnd" label={t('pages.chainProxy.pools.portEnd')}>
                <InputNumber min={0} max={65535} />
              </FormField>
            </Space>
            {network !== 'tcp' ? (
              <Space size={12} style={{ width: '100%' }} align="start">
                <FormField name="path" label={t('pages.chainProxy.pools.path')}>
                  <Input placeholder={network === 'grpc' ? 'chain-grpc' : '/chain'} />
                </FormField>
                {network !== 'grpc' ? (
                  <FormField name="transportHost" label={t('pages.chainProxy.pools.transportHost')}>
                    <Input placeholder="cdn.example.com" />
                  </FormField>
                ) : null}
              </Space>
            ) : null}
            {security === 'tls' ? (
              <Space direction="vertical" size={8} style={{ width: '100%' }}>
                <FormField name="tlsCert" label={t('pages.chainProxy.pools.tlsCert')}>
                  <Input placeholder="/etc/ssl/fullchain.pem" />
                </FormField>
                <FormField name="tlsKey" label={t('pages.chainProxy.pools.tlsKey')}>
                  <Input placeholder="/etc/ssl/privkey.pem" />
                </FormField>
                <FormField name="tlsServerName" label={t('pages.chainProxy.pools.tlsServerName')}>
                  <Input placeholder="example.com" />
                </FormField>
              </Space>
            ) : null}
            {security === 'reality' ? (
              <Space direction="vertical" size={8} style={{ width: '100%' }}>
                <FormField name="realityDest" label={t('pages.chainProxy.pools.realityDest')}>
                  <Input placeholder="www.microsoft.com:443" />
                </FormField>
                <FormField
                  name="realityServerNames"
                  label={t('pages.chainProxy.pools.realityServerNames')}
                >
                  <Input placeholder="www.microsoft.com,www.bing.com" />
                </FormField>
                <FormField
                  name="realityShortIds"
                  label={t('pages.chainProxy.pools.realityShortIds')}
                >
                  <Input placeholder="0123456789abcdef" />
                </FormField>
                <FormField
                  name="realityPrivateKey"
                  label={t('pages.chainProxy.pools.realityPrivateKey')}
                >
                  <Input placeholder="auto" />
                </FormField>
                <FormField
                  name="realityFingerprint"
                  label={t('pages.chainProxy.pools.realityFingerprint')}
                >
                  <Input placeholder="chrome" />
                </FormField>
              </Space>
            ) : null}
            <Space size={12} style={{ width: '100%' }} align="start">
              <FormField name="totalGB" label={t('pages.chainProxy.pools.totalGB')}>
                <InputNumber min={0} />
              </FormField>
              <FormField name="group" label={t('pages.chainProxy.pools.group')}>
                <Input placeholder="chain" />
              </FormField>
            </Space>
            <FormField
              name="frontTag"
              label={t('pages.chainProxy.pools.frontTag')}
              tooltip={t('pages.chainProxy.pools.frontTagHint')}
            >
              <Input placeholder="cf:p1:f:hk" />
            </FormField>
            <FormField name="probeUrl" label={t('pages.chainProxy.pools.probeUrl')}>
              <Input placeholder="https://www.cloudflare.com/cdn-cgi/trace" />
            </FormField>
            <FormField
              name="autoFailover"
              label={t('pages.chainProxy.pools.autoFailover')}
              tooltip={t('pages.chainProxy.pools.autoFailoverHint')}
              valueProp="checked"
            >
              <Switch aria-label={t('pages.chainProxy.pools.autoFailover')} />
            </FormField>
          </Form>
        </FormProvider>
      </Modal>

      {importPool ? (
        <ImportDrawer
          open
          poolId={importPool.id}
          poolName={importPool.name}
          onClose={() => setImportPool(null)}
        />
      ) : null}

      <PlanModal
        open={planPool !== null}
        pool={planPool}
        onClose={() => setPlanPool(null)}
        onApplied={onJobStarted}
      />

      {proxyPool ? <ProxyDrawer pool={proxyPool} onClose={() => setProxyPool(null)} /> : null}
    </Card>
  );
}

function ProxyDrawer({ pool, onClose }: { pool: ChainPool; onClose: () => void }) {
  const { t } = useTranslation();
  const { data, isLoading } = useChainProxies(pool.id);
  const { bulkProxies } = useChainMutations();
  const [modal, modalContextHolder] = Modal.useModal();
  const [selected, setSelected] = useState<number[]>([]);

  const runBulk = async (action: string) => {
    try {
      const res = await bulkProxies({ poolId: pool.id, ids: selected, action });
      getMessage().success(t('pages.chainProxy.toasts.proxyBulkDone', { count: selected.length }));
      setSelected([]);
      void res;
    } catch (error) {
      getMessage().error((error as Error).message);
    }
  };

  const confirmDelete = () => {
    modal.confirm({
      title: t('pages.chainProxy.pools.bulkDeleteTitle', { count: selected.length }),
      okButtonProps: { danger: true },
      onOk: () => runBulk('delete'),
    });
  };
  const columns = useMemo<ColumnsType<ChainProxy>>(
    () => [
      {
        title: t('host'),
        dataIndex: 'host',
        render: (host: string, row) => (
          <span className="chain-mono">
            {host}:{row.port}
          </span>
        ),
      },
      { title: t('pages.chainProxy.pools.label'), dataIndex: 'label', ellipsis: true },
      {
        title: t('status'),
        dataIndex: 'health',
        width: 110,
        render: (health: string) => <Tag color={healthColor(health)}>{health}</Tag>,
      },
      {
        title: t('pages.chainProxy.nodes.latency'),
        dataIndex: 'latencyMs',
        width: 90,
        render: (ms: number) => (ms > 0 ? `${ms} ms` : '—'),
      },
      { title: t('pages.chainProxy.nodes.exitIp'), dataIndex: 'exitIp', width: 140 },
    ],
    [t],
  );

  return (
    <Drawer
      open
      onClose={onClose}
      width={780}
      title={`${pool.name} · ${t('pages.chainProxy.pools.proxies')}`}
    >
      {modalContextHolder}
      {selected.length > 0 ? (
        <Space style={{ marginBottom: 12 }} wrap>
          <Tag color="blue">{t('pages.chainProxy.nodes.selected', { count: selected.length })}</Tag>
          <Button size="small" onClick={() => runBulk('enable')}>
            {t('pages.chainProxy.pools.bulkEnable')}
          </Button>
          <Button size="small" onClick={() => runBulk('disable')}>
            {t('pages.chainProxy.pools.bulkDisable')}
          </Button>
          <Button size="small" danger onClick={confirmDelete}>
            {t('pages.chainProxy.pools.bulkDelete')}
          </Button>
        </Space>
      ) : null}
      <Table
        rowKey="id"
        size="small"
        loading={isLoading}
        columns={columns}
        dataSource={data?.items ?? []}
        rowSelection={{
          selectedRowKeys: selected,
          onChange: (keys) => setSelected(keys as number[]),
        }}
        pagination={{ pageSize: 50, hideOnSinglePage: true }}
      />
    </Drawer>
  );
}
